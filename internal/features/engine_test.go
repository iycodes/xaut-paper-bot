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
