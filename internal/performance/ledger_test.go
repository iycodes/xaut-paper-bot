package performance

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"xaut-paper-bot/internal/domain"
)

func TestLedgerMigratesOpenTradeFromPreviousStateFormat(t *testing.T) {
	dir := t.TempDir()
	legacy := persisted{Open: &openTrade{Qty: 2, Record: domain.TradeRecord{EntryVWAP: 100, Quantity: 2}}}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "performance_state.json"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	ledger, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.open == nil || ledger.open.Avg != 100 || ledger.open.EntryQuantity != 2 {
		t.Fatalf("legacy open trade was not migrated: %+v", ledger.open)
	}
}

func TestLedgerComputesScaledEntryAndExitVWAP(t *testing.T) {
	l, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ctx := Context{InitialStop: 90, Regime: domain.RegimeRange, Fair: domain.FairValue{Confidence: .9}}
	fills := []domain.Fill{
		{ID: 1, Time: now, Amount: 1, Price: 100, Fee: -.01, FeeCurrency: "USD"},
		{ID: 2, Time: now.Add(time.Second), Amount: 1, Price: 110, Fee: -.001, FeeCurrency: "XAUT"},
		{ID: 3, Time: now.Add(2 * time.Second), Amount: -1, Price: 120, Fee: -.02, FeeCurrency: "USD"},
		{ID: 4, Time: now.Add(3 * time.Second), Amount: -1, Price: 130, Fee: -.02, FeeCurrency: "USD"},
	}
	closed, err := l.Process(now.Add(3*time.Second), fills, domain.AccountSnapshot{}, ctx, 130, "test exit")
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 {
		t.Fatalf("closed trades = %d, want 1", len(closed))
	}
	trade := closed[0]
	assertNear(t, "entry VWAP", trade.EntryVWAP, 105)
	assertNear(t, "exit VWAP", trade.ExitVWAP, 125)
	assertNear(t, "quantity", trade.Quantity, 2)
	assertNear(t, "gross PnL", trade.GrossPnLUSD, 40)
	assertNear(t, "fees", trade.FeesUSD, .16)
	assertNear(t, "R multiple", trade.RMultiple, (40-.16)/30)
}

func TestLedgerUpdatesExcursionsWithoutNewFills(t *testing.T) {
	l, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ctx := Context{InitialStop: 90}
	if _, err := l.Process(now, []domain.Fill{{ID: 1, Time: now, Amount: 1, Price: 100}}, domain.AccountSnapshot{}, ctx, 100, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Process(now.Add(time.Minute), nil, domain.AccountSnapshot{}, ctx, 112, ""); err != nil {
		t.Fatal(err)
	}
	closed, err := l.Process(now.Add(2*time.Minute), []domain.Fill{{ID: 2, Time: now.Add(2 * time.Minute), Amount: -1, Price: 100}}, domain.AccountSnapshot{}, ctx, 100, "exit")
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, "MFE", closed[0].MFEUSD, 12)
}

func TestLedgerAccruesEstimatedShortFunding(t *testing.T) {
	l, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ctx := Context{InitialStop: 1010, FundingDailyRate: .001}
	if _, err := l.Process(now, []domain.Fill{{ID: 1, Time: now, Amount: -1, Price: 1000}}, domain.AccountSnapshot{}, ctx, 1000, ""); err != nil {
		t.Fatal(err)
	}
	closed, err := l.Process(now.Add(24*time.Hour), []domain.Fill{{ID: 2, Time: now.Add(24 * time.Hour), Amount: 1, Price: 1000}}, domain.AccountSnapshot{}, ctx, 1000, "exit")
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, "funding", closed[0].FundingUSD, 1)
	assertNear(t, "net PnL", closed[0].NetPnLUSD, -1)
}

func assertNear(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.12f, want %.12f", name, got, want)
	}
}
