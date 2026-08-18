package features

import (
	"testing"
	"time"
	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

func TestSeededMultiTimeframeAndFlow(t *testing.T) {
	c := config.Default()
	e := New(c.Market)
	now := time.Now().UTC()
	for _, tf := range []string{"15m", "1h", "4h"} {
		cs := make([]domain.Candle, 0, 80)
		d := 15 * time.Minute
		if tf == "1h" {
			d = time.Hour
		}
		if tf == "4h" {
			d = 4 * time.Hour
		}
		for i := 80; i > 0; i-- {
			cs = append(cs, domain.Candle{Time: now.Add(-time.Duration(i) * d), Close: 4000 + float64(80-i)*2})
		}
		e.Seed(tf, cs)
	}
	d := domain.BookSnapshot{Bid: 4300, Ask: 4301, BidQty: 5, AskQty: 2, Bids: []domain.BookLevel{{Price: 4300, Amount: 5}}, Asks: []domain.BookLevel{{Price: 4301, Amount: 2}}, UpdatedAt: now}
	f := domain.FairValue{Valid: true, Price: 4290, Bid: 4289, Ask: 4291}
	tr := []domain.PublicTrade{{Time: now.Add(-time.Second), Amount: 2, Price: 4300}}
	o := e.Update(now, d, f, tr)
	if o.Trend1h <= 0 || o.Trend4h <= 0 {
		t.Fatalf("expected positive seeded trend %+v", o)
	}
	if o.OrderFlowScore <= 0 {
		t.Fatalf("expected buy flow %+v", o)
	}
}

func TestFirstLiveUpdateDoesNotDuplicateFinalSeededCandle(t *testing.T) {
	cfg := config.Default()
	e := New(cfg.Market)
	now := time.Now().UTC().Truncate(15 * time.Minute)
	e.Seed("15m", []domain.Candle{
		{Time: now.Add(-30 * time.Minute), Close: 4000},
		{Time: now.Add(-15 * time.Minute), Close: 4010},
	})
	beforeCloses, beforeReturns := e.f15.closes.Len(), e.f15.returns.Len()
	book := domain.BookSnapshot{Bid: 4019, Ask: 4021, BidQty: 1, AskQty: 1, UpdatedAt: now}
	fair := domain.FairValue{Valid: true, Price: 4020, Bid: 4019, Ask: 4021}
	e.Update(now, book, fair, nil)
	if e.f15.closes.Len() != beforeCloses || e.f15.returns.Len() != beforeReturns {
		t.Fatalf("first live update duplicated a seeded close: closes %d->%d returns %d->%d", beforeCloses, e.f15.closes.Len(), beforeReturns, e.f15.returns.Len())
	}
}

func TestStaleCachedTradesDoNotContributeFlowPersistence(t *testing.T) {
	now := time.Now().UTC()
	trades := []domain.PublicTrade{
		{Time: now.Add(-2 * time.Minute), Amount: 1, Price: 4000},
		{Time: now.Add(-2 * time.Minute), Amount: 1, Price: 4000},
		{Time: now.Add(-2 * time.Minute), Amount: 1, Price: 4000},
		{Time: now.Add(-2 * time.Minute), Amount: 1, Price: 4000},
	}
	if latestTradeFresh(now, trades, 30*time.Second) {
		t.Fatal("stale cached trades reported as fresh")
	}
	if got := flowPersistence(now, trades, time.Minute); got != 0 {
		t.Fatalf("stale persistence = %v, want 0", got)
	}
}

func TestBasisInstabilityStableBaselineIsNearOne(t *testing.T) {
	values := make([]float64, 240)
	for i := range values {
		if i%2 == 0 {
			values[i] = -1
		} else {
			values[i] = 1
		}
	}
	got := basisInstability(values)
	if got < .9 || got > 1.1 {
		t.Fatalf("stable ratio = %v, want approximately 1", got)
	}
}
