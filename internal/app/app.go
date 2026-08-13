package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
	"xaut-paper-bot/internal/exchange"
	"xaut-paper-bot/internal/execution"
	"xaut-paper-bot/internal/fairvalue"
	"xaut-paper-bot/internal/features"
	"xaut-paper-bot/internal/journal"
	"xaut-paper-bot/internal/monitor"
	"xaut-paper-bot/internal/performance"
	"xaut-paper-bot/internal/position"
	"xaut-paper-bot/internal/regime"
	"xaut-paper-bot/internal/risk"
	"xaut-paper-bot/internal/strategy"
)

type App struct {
	cfg      config.Config
	exchange exchange.Client
	journal  *journal.Journal
	store    *monitor.Store
	log      *slog.Logger

	fair        *fairvalue.Engine
	features    *features.Engine
	regime      *regime.Classifier
	strategy    *strategy.Engine
	risk        *risk.Manager
	position    *position.Tracker
	planner     *execution.Planner
	performance *performance.Ledger

	startedAt      time.Time
	account        domain.AccountSnapshot
	accountAt      time.Time
	lastSubmit     time.Time
	lastExitReason string
	mu             sync.Mutex
}

func New(cfg config.Config, ex exchange.Client, j *journal.Journal, store *monitor.Store, logger *slog.Logger) (*App, error) {
	riskManager, err := risk.New(cfg.Risk, cfg.App.DataDirectory)
	if err != nil {
		return nil, err
	}
	positionTracker, err := position.New(cfg.Risk, cfg.Execution, cfg.App.DataDirectory)
	if err != nil {
		return nil, err
	}
	ledger, err := performance.New(cfg.App.DataDirectory)
	if err != nil {
		return nil, err
	}
	return &App{
		cfg: cfg, exchange: ex, journal: j, store: store, log: logger,
		fair:     fairvalue.New(cfg.Symbols, cfg.Market, cfg.Gold),
		features: features.New(cfg.Market),
		regime:   regime.New(cfg.Market, cfg.Strategy),
		strategy: strategy.New(cfg.Strategy, cfg.Funding),
		risk:     riskManager, position: positionTracker,
		planner: execution.New(cfg.Execution, cfg.Symbols), performance: ledger,
		startedAt: time.Now().UTC(),
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	if err := a.exchange.Start(ctx); err != nil {
		return err
	}
	defer a.exchange.Close()
	a.seedTimeframes(ctx)
	if err := a.tick(ctx); err != nil {
		a.log.Warn("initial engine tick failed", "error", err)
	}
	ticker := time.NewTicker(a.cfg.App.TickInterval.Duration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if a.cfg.App.CancelOnShutdown && a.exchange.PaperVerified() {
				if err := a.exchange.CancelBotOrders(context.Background()); err != nil {
					a.log.Warn("shutdown cancel failed", "error", err)
				}
			}
			return nil
		case <-ticker.C:
			if err := a.tick(ctx); err != nil {
				a.log.Error("engine tick failed", "error", err)
			}
		}
	}
}

func (a *App) seedTimeframes(ctx context.Context) {
	for _, tf := range []string{"15m", "1h", "4h"} {
		candles, err := a.exchange.Candles(ctx, a.cfg.Symbols.XAUTUSD, tf, 180)
		if err != nil {
			a.log.Warn("seed candles failed", "timeframe", tf, "error", err)
			continue
		}
		a.features.Seed(tf, candles)
	}
}

func (a *App) tick(ctx context.Context) error {
	now := time.Now().UTC()
	books, err := a.exchange.Books(ctx)
	if err != nil {
		a.publishError(now, err)
		return err
	}
	modelDirect := books[a.cfg.Symbols.XAUTUSD]
	executionDirect := books[a.cfg.Symbols.OrderPair]
	fair := a.fair.Estimate(books, domain.GoldReference{}, now)

	trades, tradeErr := a.exchange.PublicTrades(ctx, a.cfg.Symbols.XAUTUSD, now.Add(-a.cfg.Market.TradeFlowLookback.Duration), 1000)
	if tradeErr != nil {
		a.log.Warn("public trades unavailable; micro flow degraded", "error", tradeErr)
		trades = nil
	}
	feat := a.features.Update(now, modelDirect, fair, trades)

	funding, fundingErr := a.exchange.Funding(ctx)
	if fundingErr != nil {
		funding = domain.FundingSnapshot{Symbol: a.cfg.Symbols.XAUTFunding, Valid: false, UpdatedAt: now, Reason: fundingErr.Error()}
	}

	if a.accountAt.IsZero() || now.Sub(a.accountAt) >= a.cfg.App.AccountRefresh.Duration {
		account, accountErr := a.exchange.Account(ctx, fair.Price)
		if accountErr != nil {
			if errors.Is(accountErr, exchange.ErrNoCredentials) {
				account = domain.AccountSnapshot{EquityUSD: a.cfg.Risk.CapitalBaseUSD, QuoteUSD: a.cfg.Risk.CapitalBaseUSD, Synthetic: true, UpdatedAt: now}
			} else {
				a.publishError(now, accountErr)
				return accountErr
			}
		}
		a.mu.Lock()
		a.account = account
		a.accountAt = now
		a.mu.Unlock()
	}
	a.mu.Lock()
	account := a.account
	a.mu.Unlock()

	positionEvent, err := a.position.Reconcile(now, account, executionDirect)
	if err != nil {
		a.publishError(now, fmt.Errorf("reconcile position risk state: %w", err))
		return err
	}
	if positionEvent.ExitRequired {
		a.lastExitReason = positionEvent.Reason
		if strings.Contains(strings.ToLower(positionEvent.Reason), "stop") {
			q := a.position.State().QuantityXAUT
			a.strategy.MarkStopped(q, now)
		}
	}

	// Drawdown logic receives true uncapped equity; only position sizing is capped.
	observedEquity := account.EquityUSD
	if account.Synthetic || observedEquity <= 0 {
		observedEquity = a.cfg.Risk.CapitalBaseUSD
	}
	if err := a.risk.ObserveEquity(now, observedEquity); err != nil {
		a.log.Warn("persist risk state", "error", err)
	}

	currentExposure := exposure(account, fair.Price, a.cfg.Risk.CapitalBaseUSD)
	reg, regReason := a.regime.Classify(now, modelDirect, fair, feat)
	signal := a.strategy.Signal(strategy.Input{Now: now, Regime: reg, RegimeReason: regReason, Features: feat, Fair: fair, Direct: modelDirect, Funding: funding, CurrentExposure: currentExposure})
	posState := a.position.State()
	decision := a.risk.Evaluate(now, signal, feat, fair, executionDirect, account, posState, a.cfg.App.FlattenOnHardHalt)

	// Consume actual account fills before planning a new order. This drives closed
	// P&L, consecutive-loss controls, and paper performance attribution.
	if a.exchange.PaperVerified() {
		fills, fillErr := a.exchange.Fills(ctx, a.performance.Since())
		if fillErr != nil {
			a.log.Warn("account fills unavailable", "error", fillErr)
		} else if len(fills) > 0 {
			ctxPerf := performance.Context{Regime: reg, Features: feat, Signal: signal, Fair: fair, InitialStop: posState.StopPrice}
			closed, lerr := a.performance.Process(now, fills, account, ctxPerf, executionDirect.Mid(), a.lastExitReason)
			if lerr != nil {
				a.log.Warn("performance ledger", "error", lerr)
			}
			for _, tr := range closed {
				if err := a.risk.RecordClosedTrade(tr.NetPnLUSD); err != nil {
					a.log.Warn("persist closed-trade risk state", "error", err)
				}
				if strings.Contains(strings.ToLower(tr.ExitReason), "stop") {
					dir := 1.0
					if tr.Direction == "short" {
						dir = -1
					}
					a.strategy.MarkStopped(dir, tr.ExitTime)
				}
				_ = a.journal.Append("trade_closed", tr)
				a.lastExitReason = ""
			}
		}
	}

	plan := domain.ExecutionPlan{Reason: decision.Reason}
	plannerInput := execution.Input{Now: now, Account: account, Target: decision.Target, Position: posState, Direct: executionDirect, RecentVolumeUSD: recentVolumeUSD(trades, now.Add(-time.Minute)), BotGID: a.cfg.Execution.GroupID}
	if positionEvent.ExitRequired {
		decision.Allowed = true
		decision.Flatten = true
		decision.Reason = positionEvent.Reason
		decision.Target = domain.Target{Reason: positionEvent.Reason}
		plannerInput.Target = domain.Target{}
		plannerInput.Urgent = true
		plan = a.planner.Plan(plannerInput)
	} else if decision.Halt && decision.Flatten {
		a.lastExitReason = decision.Reason
		plannerInput.Target = domain.Target{}
		plannerInput.Urgent = true
		plan = a.planner.Plan(plannerInput)
	} else if signal.NoNewEntries || !decision.Allowed {
		ids := botOrderIDs(account.OpenOrders, a.cfg.Execution.GroupID)
		if len(ids) > 0 {
			plan = domain.ExecutionPlan{CancelOrderIDs: ids, Reason: "entries blocked; cancel working entry order"}
		} else {
			// Even when entries are blocked, keep an existing position protected.
			plannerInput.Target = domain.Target{QuantityXAUT: account.NetXAUT()}
			plan = a.planner.Plan(plannerInput)
		}
	} else {
		plan = a.planner.Plan(plannerInput)
	}

	if plan.Intent != nil && isOpeningIntent(*plan.Intent) {
		if err := a.position.SetEntryContext(reg, feat.BasisZ, feat.TrendScore); err != nil {
			return err
		}
	}
	if err := a.execute(ctx, now, plan); err != nil {
		a.publish(now, books, fair, funding, feat, reg, signal, account, decision, plan, err)
		return err
	}
	a.publish(now, books, fair, funding, feat, reg, signal, account, decision, plan, nil)
	return nil
}

func (a *App) execute(ctx context.Context, now time.Time, plan domain.ExecutionPlan) error {
	if len(plan.CancelOrderIDs) > 0 {
		if !a.exchange.PaperVerified() {
			_ = a.journal.Append("planned_cancel_without_paper_auth", plan)
			return nil
		}
		for _, id := range plan.CancelOrderIDs {
			if err := a.exchange.Cancel(ctx, id); err != nil {
				_ = a.journal.Append("cancel_error", map[string]any{"id": id, "error": err.Error()})
				return err
			}
			_ = a.journal.Append("order_cancelled", map[string]any{"id": id, "reason": plan.Reason})
		}
		a.accountAt = time.Time{}
		return nil
	}
	if plan.Intent == nil {
		return nil
	}
	if !a.exchange.OrdersEnabled() {
		_ = a.journal.Append("planned_order_observe_only", plan.Intent)
		return nil
	}
	if !a.lastSubmit.IsZero() && now.Sub(a.lastSubmit) < a.cfg.Execution.MinimumSubmitInterval.Duration {
		return nil
	}
	if isOpeningIntent(*plan.Intent) && plan.Intent.StopDistance > 0 {
		if err := a.position.Arm(plan.Intent.StopDistance); err != nil {
			return fmt.Errorf("arm software stop before order submission: %w", err)
		}
	}
	response, err := a.exchange.Submit(ctx, *plan.Intent)
	if err != nil {
		_ = a.journal.Append("submit_error", map[string]any{"intent": plan.Intent, "error": err.Error(), "response": response})
		return err
	}
	a.lastSubmit = now
	a.accountAt = time.Time{}
	_ = a.journal.Append("order_submitted", map[string]any{"intent": plan.Intent, "response": response})
	return nil
}

func (a *App) publish(now time.Time, books map[string]domain.BookSnapshot, fair domain.FairValue, funding domain.FundingSnapshot, feat domain.Features, reg domain.Regime, signal domain.Signal, account domain.AccountSnapshot, decision domain.RiskDecision, plan domain.ExecutionPlan, err error) {
	mode := "public-observe"
	if a.exchange.PaperVerified() {
		mode = "paper-observe"
	}
	if a.exchange.OrdersEnabled() {
		mode = "paper-live"
	}
	status := domain.RuntimeStatus{StartedAt: a.startedAt, UpdatedAt: now, Mode: mode, Ready: fair.Valid && feat.Warm && reg != domain.RegimeNoTrade && reg != domain.RegimeTransition, PaperVerified: a.exchange.PaperVerified(), OrdersEnabled: a.exchange.OrdersEnabled(), Books: books, FairValue: fair, Funding: funding, Features: feat, Regime: reg, Signal: signal, Account: account, Risk: decision, Position: a.position.State(), Execution: plan, LastExchangeEvent: a.exchange.LastEvent()}
	if err != nil {
		status.LastError = err.Error()
	}
	a.store.Set(status)
}
func (a *App) publishError(now time.Time, err error) {
	st := a.store.Get()
	st.UpdatedAt = now
	st.Ready = false
	st.LastError = err.Error()
	st.LastExchangeEvent = a.exchange.LastEvent()
	a.store.Set(st)
}
func exposure(a domain.AccountSnapshot, fair, cap float64) float64 {
	if fair <= 0 {
		return 0
	}
	eq := a.EquityUSD
	if a.Synthetic || eq <= 0 {
		eq = cap
	}
	eq = math.Min(eq, cap)
	if eq <= 0 {
		return 0
	}
	return a.NetXAUT() * fair / eq
}
func recentVolumeUSD(trades []domain.PublicTrade, since time.Time) float64 {
	var v float64
	for _, t := range trades {
		if !t.Time.Before(since) {
			v += math.Abs(t.Amount) * t.Price
		}
	}
	return v
}
func botOrderIDs(orders []domain.OpenOrder, gid int64) []int64 {
	out := []int64{}
	for _, o := range orders {
		if o.GID == gid {
			out = append(out, o.ID)
		}
	}
	return out
}
func isOpeningIntent(in domain.OrderIntent) bool {
	return (in.Venue == domain.VenueSpot && in.Side == domain.SideBuy) || (in.Venue == domain.VenueMargin && in.Side == domain.SideSell && !in.CloseOnly)
}
func (a *App) String() string { return fmt.Sprintf("%s (%s)", a.cfg.App.Name, a.store.Get().Mode) }
