package regime

import (
	"testing"
	"time"
	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

func validInputs() (config.Config, time.Time, domain.BookSnapshot, domain.FairValue, domain.Features) {
	c := config.Default()
	now := time.Now().UTC()
	b := domain.BookSnapshot{Bid: 4300, Ask: 4301, BidQty: 1, AskQty: 1, UpdatedAt: now}
	fv := domain.FairValue{Valid: true, Price: 4300.5, Bid: 4299, Ask: 4302, RouteDispersionBPS: 1}
	f := domain.Features{Warm: true, SpreadBPS: 2, Volatility: .002, VolatilityRatio: 1, BasisInstability: .2}
	return c, now, b, fv, f
}
func TestTrendTakesPriorityOverDislocation(t *testing.T) {
	c, now, b, fv, f := validInputs()
	f.TrendScore = .8
	f.BasisZ = 3
	r, _ := New(c.Market, c.Strategy).Classify(now, b, fv, f)
	if r != domain.RegimeTrend {
		t.Fatalf("expected trend, got %s", r)
	}
}
func TestTransitionBlocksMeanReversion(t *testing.T) {
	c, now, b, fv, f := validInputs()
	f.BasisZ = 3
	f.VolatilityRatio = c.Market.TransitionVolRatio + 0.1
	r, _ := New(c.Market, c.Strategy).Classify(now, b, fv, f)
	if r != domain.RegimeTransition {
		t.Fatalf("expected transition, got %s", r)
	}
}
