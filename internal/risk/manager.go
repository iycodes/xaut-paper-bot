package risk

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

type PersistentState struct {
	DayKey             string  `json:"day_key"`
	WeekKey            string  `json:"week_key"`
	DayStartEquityUSD  float64 `json:"day_start_equity_usd"`
	WeekStartEquityUSD float64 `json:"week_start_equity_usd"`
	HighWaterEquityUSD float64 `json:"high_water_equity_usd"`
	ConsecutiveLosses  int     `json:"consecutive_losses"`
	HardHalt           bool    `json:"hard_halt"`
	HaltReason         string  `json:"halt_reason,omitempty"`
}
type Manager struct {
	cfg       config.RiskConfig
	statePath string
	mu        sync.Mutex
	state     PersistentState
}

func New(cfg config.RiskConfig, dataDir string) (*Manager, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	m := &Manager{cfg: cfg, statePath: filepath.Join(dataDir, "risk_state.json")}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// ObserveEquity always receives uncapped account equity. Position sizing may use
// a capped risk base, but drawdown protection must see gains and losses above it.
func (m *Manager) ObserveEquity(now time.Time, equity float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if equity <= 0 || math.IsNaN(equity) || math.IsInf(equity, 0) {
		return nil
	}
	day := now.UTC().Format("2006-01-02")
	y, w := now.UTC().ISOWeek()
	wk := fmt.Sprintf("%04d-W%02d", y, w)
	changed := false
	if m.state.DayKey != day || m.state.DayStartEquityUSD <= 0 {
		m.state.DayKey = day
		m.state.DayStartEquityUSD = equity
		changed = true
	}
	if m.state.WeekKey != wk || m.state.WeekStartEquityUSD <= 0 {
		m.state.WeekKey = wk
		m.state.WeekStartEquityUSD = equity
		changed = true
	}
	if equity > m.state.HighWaterEquityUSD {
		m.state.HighWaterEquityUSD = equity
		changed = true
	}
	if !m.state.HardHalt {
		switch {
		case m.state.DayStartEquityUSD-equity >= m.cfg.DailyHardLossUSD:
			m.state.HardHalt = true
			m.state.HaltReason = fmt.Sprintf("daily loss reached $%.2f", m.state.DayStartEquityUSD-equity)
			changed = true
		case m.state.WeekStartEquityUSD-equity >= m.cfg.WeeklyHardLossUSD:
			m.state.HardHalt = true
			m.state.HaltReason = fmt.Sprintf("weekly loss reached $%.2f", m.state.WeekStartEquityUSD-equity)
			changed = true
		case m.state.HighWaterEquityUSD-equity >= m.cfg.MaximumDrawdownUSD:
			m.state.HardHalt = true
			m.state.HaltReason = fmt.Sprintf("drawdown reached $%.2f", m.state.HighWaterEquityUSD-equity)
			changed = true
		case m.cfg.MaximumConsecutiveLosses > 0 && m.state.ConsecutiveLosses >= m.cfg.MaximumConsecutiveLosses:
			m.state.HardHalt = true
			m.state.HaltReason = fmt.Sprintf("%d consecutive losses", m.state.ConsecutiveLosses)
			changed = true
		}
	}
	if changed {
		return m.saveLocked()
	}
	return nil
}
func (m *Manager) RecordClosedTrade(pnl float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pnl < 0 {
		m.state.ConsecutiveLosses++
	} else if pnl > 0 {
		m.state.ConsecutiveLosses = 0
	}
	return m.saveLocked()
}

func (m *Manager) Evaluate(now time.Time, signal domain.Signal, f domain.Features, fair domain.FairValue, direct domain.BookSnapshot, account domain.AccountSnapshot, pos domain.PositionState, flattenOnHardHalt bool) domain.RiskDecision {
	m.mu.Lock()
	state := m.state
	m.mu.Unlock()
	d := domain.RiskDecision{Reason: "risk checks passed", Throttle: 1}
	if state.HardHalt {
		d.Halt = true
		d.Flatten = flattenOnHardHalt
		d.Reason = state.HaltReason
		return d
	}
	if m.haltFileExists() {
		d.Halt = true
		d.Flatten = flattenOnHardHalt
		d.Reason = "operator HALT file present"
		return d
	}
	if !account.Synthetic && (account.UpdatedAt.IsZero() || now.Sub(account.UpdatedAt) > m.cfg.AccountMaximumAge.Duration) {
		d.Reason = "account snapshot is stale"
		return d
	}
	if account.MarginXAUT > 1e-9 {
		d.Halt = true
		d.Reason = "unsupported margin long detected"
		return d
	}
	if !fair.Valid || fair.Price <= 0 || !direct.Valid() {
		d.Reason = "fair value or direct book invalid"
		return d
	}

	drawdownEquity := account.EquityUSD
	if account.Synthetic || drawdownEquity <= 0 {
		drawdownEquity = m.cfg.CapitalBaseUSD
	}
	d.DrawdownEquity = drawdownEquity
	equity := math.Min(drawdownEquity, m.cfg.CapitalBaseUSD)
	if equity <= 0 {
		d.Reason = "effective sizing equity not positive"
		return d
	}
	d.EffectiveEquity = equity
	dayLoss := math.Max(0, state.DayStartEquityUSD-drawdownEquity)
	switch {
	case dayLoss >= m.cfg.DailySoftLoss2USD:
		d.Throttle = clamp(m.cfg.DailySoftLoss2Throttle, 0, 1)
	case dayLoss >= m.cfg.DailySoftLoss1USD:
		d.Throttle = clamp(m.cfg.DailySoftLoss1Throttle, 0, 1)
	}

	currentQty := account.NetXAUT()
	currentExposure := currentQty * fair.Price / equity
	if signal.NoNewEntries {
		d.Allowed = true
		d.Target = domain.Target{Exposure: currentExposure, NotionalUSD: math.Abs(currentQty * fair.Price), QuantityXAUT: currentQty, StopPrice: pos.StopPrice, Reason: "hold current position while entries blocked"}
		d.Reason = signal.Reason
		return d
	}

	desiredExposure := clamp(signal.DesiredExposure, -m.cfg.AbsoluteGrossExposure, m.cfg.AbsoluteGrossExposure) * d.Throttle
	stopDistance := math.Max(m.cfg.MinimumStopFraction, f.Volatility*m.cfg.VolatilityStopMultiplier)
	riskBudget := equity * m.cfg.RiskPerTradeFraction * d.Throttle
	desiredNotional := math.Abs(desiredExposure) * equity
	absoluteCap := equity * m.cfg.AbsoluteGrossExposure
	liquidityCap := direct.DepthQuote * m.cfg.LiquidityParticipation
	if liquidityCap <= 0 {
		liquidityCap = desiredNotional
	}
	riskSizedNotional := riskBudget / stopDistance
	targetNotional := minPositive(desiredNotional, riskSizedNotional, liquidityCap, absoluteCap)
	pending := pendingOpeningNotional(account.OpenOrders, fair.Price)
	targetNotional = math.Min(targetNotional, math.Max(0, absoluteCap-pending))

	isReduction := desiredExposure == 0 || (sameSign(desiredExposure, currentExposure) && math.Abs(desiredExposure) < math.Abs(currentExposure))
	if isReduction {
		targetNotional = math.Min(desiredNotional, absoluteCap)
	}
	if targetNotional > 0 && targetNotional < m.cfg.MinimumOrderNotionalUSD {
		targetNotional = 0
	}
	sign := 0.0
	if desiredExposure > 0 {
		sign = 1
	} else if desiredExposure < 0 {
		sign = -1
	}
	targetQty := 0.0
	if fair.Price > 0 {
		targetQty = sign * targetNotional / fair.Price
	}

	// Simulate total loss to stop after the target is filled, including existing
	// inventory. This is the authoritative open-risk constraint.
	stopPrice := 0.0
	if sign > 0 {
		stopPrice = fair.Price * (1 - stopDistance)
	} else if sign < 0 {
		stopPrice = fair.Price * (1 + stopDistance)
	}
	entryRef := fair.Price
	if sameSign(targetQty, pos.QuantityXAUT) && pos.AverageEntry > 0 {
		old := math.Abs(pos.QuantityXAUT)
		newq := math.Abs(targetQty)
		if newq > 0 {
			added := math.Max(0, newq-old)
			entryRef = (old*pos.AverageEntry + added*fair.Price) / math.Max(newq, old)
		}
	}
	actualRisk := 0.0
	if targetQty > 0 && stopPrice > 0 {
		actualRisk = math.Abs(targetQty) * math.Max(0, entryRef-stopPrice)
	} else if targetQty < 0 && stopPrice > 0 {
		actualRisk = math.Abs(targetQty) * math.Max(0, stopPrice-entryRef)
	}
	openRiskCap := equity * m.cfg.MaximumOpenRiskFraction
	if !isReduction && actualRisk > openRiskCap && actualRisk > 0 {
		scale := openRiskCap / actualRisk
		targetNotional *= scale
		targetQty *= scale
		actualRisk = openRiskCap
	}
	if !isReduction && actualRisk > riskBudget+1e-6 {
		scale := riskBudget / actualRisk
		targetNotional *= scale
		targetQty *= scale
		actualRisk = riskBudget
	}

	currentNotional := math.Abs(currentQty * fair.Price)
	worst := math.Max(targetNotional, currentNotional) + pending
	if worst > absoluteCap+1e-6 {
		d.Reason = fmt.Sprintf("worst-case exposure $%.2f exceeds hard cap $%.2f", worst, absoluteCap)
		return d
	}
	targetExposure := 0.0
	if equity > 0 {
		targetExposure = sign * targetNotional / equity
	}
	d.Allowed = true
	d.Target = domain.Target{Exposure: targetExposure, NotionalUSD: targetNotional, QuantityXAUT: targetQty, StopDistance: stopDistance, StopPrice: stopPrice, RiskBudgetUSD: riskBudget, ActualStopRiskUSD: actualRisk, Reason: fmt.Sprintf("target $%.2f; actual stop risk $%.2f/$%.2f; throttle %.2f", targetNotional, actualRisk, riskBudget, d.Throttle)}
	if signal.ExpectedEdgeBPS > 0 {
		d.Target.Reason += fmt.Sprintf("; net edge %.1f bps", signal.ExpectedEdgeBPS)
	}
	return d
}

func pendingOpeningNotional(orders []domain.OpenOrder, price float64) float64 {
	var n float64
	for _, o := range orders {
		if (o.Venue == domain.VenueSpot && o.RemainingAmount > 0) || (o.Venue == domain.VenueMargin && o.RemainingAmount < 0) {
			n += math.Abs(o.RemainingAmount) * price
		}
	}
	return n
}
func sameSign(a, b float64) bool { return (a > 0 && b > 0) || (a < 0 && b < 0) }
func minPositive(v ...float64) float64 {
	m := math.Inf(1)
	for _, x := range v {
		if x >= 0 && x < m {
			m = x
		}
	}
	if math.IsInf(m, 1) {
		return 0
	}
	return m
}
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func (m *Manager) haltFileExists() bool {
	if m.cfg.HaltFile == "" {
		return false
	}
	_, err := os.Stat(m.cfg.HaltFile)
	return err == nil
}
func (m *Manager) load() error {
	data, err := os.ReadFile(m.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &m.state); err != nil {
		return fmt.Errorf("decode risk state: %w", err)
	}
	return nil
}
func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.statePath)
}
func (m *Manager) State() PersistentState { m.mu.Lock(); defer m.mu.Unlock(); return m.state }

var _ = strings.Builder{}
