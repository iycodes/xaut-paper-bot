package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
	"xaut-paper-bot/internal/journal"
	"xaut-paper-bot/internal/monitor"
	"xaut-paper-bot/internal/performance"
)

type orderingExchange struct {
	cfg     config.Config
	now     time.Time
	fills   []domain.Fill
	submits int
}

func (f *orderingExchange) Start(context.Context) error { return nil }
func (f *orderingExchange) Close() error                { return nil }
func (f *orderingExchange) Books(context.Context) (map[string]domain.BookSnapshot, error) {
	book := func(symbol string, bid, ask float64) domain.BookSnapshot {
		return domain.BookSnapshot{Symbol: symbol, Bid: bid, Ask: ask, BidQty: 10, AskQty: 10, DepthQuote: 100_000, UpdatedAt: f.now}
	}
	return map[string]domain.BookSnapshot{
		f.cfg.Symbols.OrderPair: book(f.cfg.Symbols.OrderPair, 99.9, 100.1),
		f.cfg.Symbols.XAUTUSD:   book(f.cfg.Symbols.XAUTUSD, 99.9, 100.1),
		f.cfg.Symbols.XAUTUST:   book(f.cfg.Symbols.XAUTUST, 99.9, 100.1),
		f.cfg.Symbols.USTUSD:    book(f.cfg.Symbols.USTUSD, .9999, 1.0001),
		f.cfg.Symbols.XAUTBTC:   book(f.cfg.Symbols.XAUTBTC, .999, 1.001),
		f.cfg.Symbols.BTCUSD:    book(f.cfg.Symbols.BTCUSD, 100, 100.01),
	}, nil
}
func (f *orderingExchange) PublicTrades(context.Context, string, time.Time, int) ([]domain.PublicTrade, error) {
	return nil, nil
}
func (f *orderingExchange) Candles(context.Context, string, string, int) ([]domain.Candle, error) {
	return nil, nil
}
func (f *orderingExchange) Funding(context.Context) (domain.FundingSnapshot, error) {
	return domain.FundingSnapshot{Valid: true, UpdatedAt: f.now, DailyRate: .0003}, nil
}
func (f *orderingExchange) Fills(context.Context, time.Time) ([]domain.Fill, error) {
	return f.fills, nil
}
func (f *orderingExchange) Account(context.Context, float64) (domain.AccountSnapshot, error) {
	return domain.AccountSnapshot{EquityUSD: 30_000, QuoteUSD: 30_000, Paper: true, UpdatedAt: f.now}, nil
}
func (f *orderingExchange) Submit(context.Context, domain.OrderIntent) (string, error) {
	f.submits++
	return "ok", nil
}
func (f *orderingExchange) Cancel(context.Context, int64) error { return nil }
func (f *orderingExchange) CancelBotOrders(context.Context) error {
	return nil
}
func (f *orderingExchange) PaperVerified() bool  { return true }
func (f *orderingExchange) OrdersEnabled() bool  { return true }
func (f *orderingExchange) LastEvent() time.Time { return f.now }

func TestFinalLossIsAppliedBeforeCurrentTickRiskDecision(t *testing.T) {
	now := time.Now().UTC()
	cfg := config.Default()
	cfg.App.DataDirectory = t.TempDir()
	cfg.Risk.HaltFile = cfg.App.DataDirectory + "/HALT"
	exchange := &orderingExchange{cfg: cfg, now: now, fills: []domain.Fill{{ID: 2, Time: now.Add(-time.Second), Amount: -1, Price: 90}}}
	j, err := journal.New(cfg.App.DataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	store := monitor.NewStore(now)
	application, err := New(cfg, exchange, j, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.performance.Process(now.Add(-2*time.Second), []domain.Fill{{ID: 1, Time: now.Add(-2 * time.Second), Amount: 1, Price: 100}}, domain.AccountSnapshot{}, performance.Context{InitialStop: 95}, 100, ""); err != nil {
		t.Fatal(err)
	}
	if err := application.risk.RecordClosedTrade(-1); err != nil {
		t.Fatal(err)
	}
	if err := application.risk.RecordClosedTrade(-1); err != nil {
		t.Fatal(err)
	}
	if err := application.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := store.Get()
	if !status.Risk.Halt || !application.risk.State().HardHalt {
		t.Fatalf("final loss was not visible to current decision: risk=%+v state=%+v", status.Risk, application.risk.State())
	}
	if exchange.submits != 0 {
		t.Fatalf("submitted %d order(s) after final allowed loss", exchange.submits)
	}
}
