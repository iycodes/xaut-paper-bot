package app

import (
	"context"
	"strings"
	"time"

	"xaut-paper-bot/internal/domain"
)

const maximumPollBackoff = 5 * time.Minute

type publicDataPoller interface {
	PublicTrades(context.Context, string, time.Time, int) ([]domain.PublicTrade, error)
	Funding(context.Context) (domain.FundingSnapshot, error)
}

type pollSchedule struct {
	next    time.Time
	backoff time.Duration
}

func (p *pollSchedule) due(now time.Time) bool {
	return p.next.IsZero() || !now.Before(p.next)
}

func (p *pollSchedule) succeeded(now time.Time, interval time.Duration) {
	p.backoff = 0
	p.next = now.Add(interval)
}

func (p *pollSchedule) failed(now time.Time, interval time.Duration, rateLimited bool) time.Duration {
	floor := interval
	if floor < time.Second {
		floor = time.Second
	}
	if rateLimited && floor < time.Minute {
		floor = time.Minute
	}
	if p.backoff < floor {
		p.backoff = floor
	} else {
		p.backoff *= 2
	}
	if p.backoff > maximumPollBackoff {
		p.backoff = maximumPollBackoff
	}
	p.next = now.Add(p.backoff)
	return p.backoff
}

func (a *App) tradesForTick(ctx context.Context, now time.Time) []domain.PublicTrade {
	if now.Before(a.publicBlocked) || !a.marketPoll.due(now) {
		return a.marketTrades
	}
	trades, err := a.publicData.PublicTrades(ctx, a.cfg.Symbols.XAUTUSD, now.Add(-a.cfg.Market.TradeFlowLookback.Duration), 1000)
	if err != nil {
		rateLimited := isRateLimitError(err.Error())
		retry := a.marketPoll.failed(now, a.cfg.Market.PublicTradesRefresh.Duration, rateLimited)
		if rateLimited {
			a.blockPublicEndpointUntil(now.Add(retry))
		}
		a.log.Warn("public trades refresh failed; using cached trade flow", "error", err, "retry_after", retry, "cached_trades", len(a.marketTrades))
		return a.marketTrades
	}
	a.marketTrades = trades
	a.marketPoll.succeeded(now, a.cfg.Market.PublicTradesRefresh.Duration)
	return a.marketTrades
}

func (a *App) fundingForTick(ctx context.Context, now time.Time) domain.FundingSnapshot {
	if now.Before(a.publicBlocked) || !a.fundingPoll.due(now) {
		return a.funding
	}
	funding, err := a.publicData.Funding(ctx)
	if err != nil {
		rateLimited := isRateLimitError(err.Error())
		retry := a.fundingPoll.failed(now, a.cfg.Funding.RefreshInterval.Duration, rateLimited)
		if rateLimited {
			a.blockPublicEndpointUntil(now.Add(retry))
		}
		a.log.Warn("funding refresh failed; using cached funding", "error", err, "retry_after", retry, "cached_valid", a.funding.Valid)
		if a.funding.UpdatedAt.IsZero() {
			a.funding = domain.FundingSnapshot{Symbol: a.cfg.Symbols.XAUTFunding, Valid: false, UpdatedAt: now, Reason: err.Error()}
		}
		return a.funding
	}
	a.funding = funding
	a.fundingPoll.succeeded(now, a.cfg.Funding.RefreshInterval.Duration)
	return a.funding
}

func (a *App) blockPublicEndpointUntil(until time.Time) {
	if until.After(a.publicBlocked) {
		a.publicBlocked = until
	}
}

func isRateLimitError(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "429") || strings.Contains(message, "ratelimit") || strings.Contains(message, "rate limit") || strings.Contains(message, "11010")
}
