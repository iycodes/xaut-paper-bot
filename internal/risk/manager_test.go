package risk

import (
	"math"
	"testing"
	"time"
	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

func TestPendingOrderCountsOnceAgainstFinalTarget(t *testing.T) {
	c := config.Default()
	c.Risk.HaltFile = t.TempDir() + "/HALT"
	now := time.Now().UTC()
	fv := domain.FairValue{Valid: true, Price: 100}
	book := domain.BookSnapshot{Bid: 99, Ask: 101, DepthQuote: 1_000_000, BidQty: 1, AskQty: 1, UpdatedAt: now}
	signal := domain.Signal{DesiredExposure: .5, ExpectedEdgeBPS: 20}
	features := domain.Features{Volatility: .001}

	without, err := New(c.Risk, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = without.ObserveEquity(now, 30_000)
	baseAccount := domain.AccountSnapshot{EquityUSD: 30_000, QuoteUSD: 30_000, UpdatedAt: now}
	base := without.Evaluate(now, signal, features, fv, book, baseAccount, domain.PositionState{}, true)

	with, err := New(c.Risk, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = with.ObserveEquity(now, 30_000)
	pendingAccount := baseAccount
	pendingAccount.OpenOrders = []domain.OpenOrder{{Venue: domain.VenueSpot, RemainingAmount: 10}}
	got := with.Evaluate(now, signal, features, fv, book, pendingAccount, domain.PositionState{}, true)
	if !got.Allowed || math.Abs(got.Target.NotionalUSD-base.Target.NotionalUSD) > 1e-9 {
		t.Fatalf("pending order changed final target: base=%+v pending=%+v", base, got)
	}
}

func TestPendingOrderProjectedExposureStillEnforcesHardCap(t *testing.T) {
	c := config.Default()
	c.Risk.HaltFile = t.TempDir() + "/HALT"
	now := time.Now().UTC()
	m, err := New(c.Risk, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = m.ObserveEquity(now, 30_000)
	account := domain.AccountSnapshot{EquityUSD: 30_000, QuoteUSD: 30_000, UpdatedAt: now, OpenOrders: []domain.OpenOrder{{Venue: domain.VenueSpot, RemainingAmount: 301}}}
	got := m.Evaluate(now, domain.Signal{DesiredExposure: .5}, domain.Features{Volatility: .001}, domain.FairValue{Valid: true, Price: 100}, domain.BookSnapshot{Bid: 99, Ask: 101, DepthQuote: 1_000_000, BidQty: 1, AskQty: 1, UpdatedAt: now}, account, domain.PositionState{}, true)
	if got.Allowed || got.Reason == "" {
		t.Fatalf("projected pending exposure should exceed hard cap: %+v", got)
	}
}

func TestStopRiskAndOneXCap(t *testing.T) {
	c := config.Default()
	c.Risk.HaltFile = t.TempDir() + "/HALT"
	m, err := New(c.Risk, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	acct := domain.AccountSnapshot{EquityUSD: 30000, QuoteUSD: 30000, UpdatedAt: now}
	_ = m.ObserveEquity(now, 30000)
	sig := domain.Signal{DesiredExposure: .8, ExpectedEdgeBPS: 20}
	f := domain.Features{Volatility: .01}
	fv := domain.FairValue{Valid: true, Price: 4300}
	d := domain.BookSnapshot{Bid: 4299, Ask: 4301, DepthQuote: 100000, BidQty: 1, AskQty: 1, UpdatedAt: now}
	r := m.Evaluate(now, sig, f, fv, d, acct, domain.PositionState{}, true)
	if !r.Allowed {
		t.Fatalf("not allowed %s", r.Reason)
	}
	if r.Target.ActualStopRiskUSD > 30000*c.Risk.RiskPerTradeFraction+.01 {
		t.Fatalf("risk too high %+v", r.Target)
	}
	if r.Target.NotionalUSD > 30000*c.Risk.AbsoluteGrossExposure+.01 {
		t.Fatal("1x cap exceeded")
	}
}
func TestDrawdownUsesActualEquityAboveCapitalBase(t *testing.T) {
	c := config.Default()
	c.Risk.HaltFile = t.TempDir() + "/HALT"
	m, _ := New(c.Risk, t.TempDir())
	now := time.Now().UTC()
	_ = m.ObserveEquity(now, 34000)
	_ = m.ObserveEquity(now.Add(time.Minute), 32400)
	if !m.State().HardHalt {
		t.Fatal("expected $1600 drawdown halt from actual high-water")
	}
}

func TestFinalConsecutiveLossLatchesHaltImmediately(t *testing.T) {
	c := config.Default()
	c.Risk.HaltFile = t.TempDir() + "/HALT"
	c.Risk.MaximumConsecutiveLosses = 3
	m, err := New(c.Risk, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := m.RecordClosedTrade(-1); err != nil {
			t.Fatal(err)
		}
	}
	state := m.State()
	if !state.HardHalt || state.HaltReason != "3 consecutive losses" {
		t.Fatalf("halt not latched on final loss: %+v", state)
	}
}
