package strategy

import (
	"strings"
	"testing"
	"time"
	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

func base() (config.Config, Input) {
	c := config.Default()
	now := time.Now().UTC()
	in := Input{Now: now, Regime: domain.RegimeDislocation, Features: domain.Features{BasisScore: -1, BasisZ: 2.5, TrendScore: 0, MicroScore: -.2, Volatility: .005}, Fair: domain.FairValue{Valid: true, Bid: 4290, Ask: 4292, Price: 4291, Confidence: .95, UncertaintyBPS: 1}, Direct: domain.BookSnapshot{Bid: 4310, Ask: 4311, BidQty: 1, AskQty: 1, UpdatedAt: now}, Funding: domain.FundingSnapshot{Valid: true, DailyRate: .0001, UpdatedAt: now}}
	return c, in
}
func TestShortRequiresEconomicEdge(t *testing.T) {
	c, in := base()
	s := New(c.Strategy, c.Funding).Signal(in)
	if s.DesiredExposure >= 0 {
		t.Fatalf("expected short %+v", s)
	}
}
func TestStaleFundingBlocksShort(t *testing.T) {
	c, in := base()
	in.Funding.Valid = false
	s := New(c.Strategy, c.Funding).Signal(in)
	if s.DesiredExposure != 0 || !s.NoNewEntries {
		t.Fatalf("expected funding block %+v", s)
	}
}
func TestStopReentryBlockedUntilReset(t *testing.T) {
	c, in := base()
	e := New(c.Strategy, c.Funding)
	e.MarkStopped(-1, in.Now)
	s := e.Signal(in)
	if !strings.Contains(s.Reason, "reentry") {
		t.Fatalf("expected reentry block %+v", s)
	}
}
