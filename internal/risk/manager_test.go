package risk

import (
	"testing"
	"time"
	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

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
