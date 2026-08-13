package fairvalue

import (
	"testing"
	"time"
	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

func TestExecutableSides(t *testing.T) {
	c := config.Default()
	now := time.Now().UTC()
	b := map[string]domain.BookSnapshot{
		c.Symbols.XAUTUST: {Bid: 4300, Ask: 4302, BidQty: 1, AskQty: 1, UpdatedAt: now}, c.Symbols.USTUSD: {Bid: .999, Ask: 1.001, BidQty: 1, AskQty: 1, UpdatedAt: now}, c.Symbols.XAUTBTC: {Bid: .05, Ask: .0501, BidQty: 1, AskQty: 1, UpdatedAt: now}, c.Symbols.BTCUSD: {Bid: 86000, Ask: 86100, BidQty: 1, AskQty: 1, UpdatedAt: now}}
	f := New(c.Symbols, c.Market, c.Gold).Estimate(b, domain.GoldReference{}, now)
	if !f.Valid {
		t.Fatalf("invalid: %+v", f)
	}
	if f.Bid <= 0 || f.Ask <= f.Bid {
		t.Fatalf("bad executable fair value %+v", f)
	}
}
