package bitfinex

import (
	"testing"
	"time"

	"github.com/bitfinexcom/bitfinex-api-go/pkg/models/book"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

func TestBuildBookSnapshotNormalizesQuantitySigns(t *testing.T) {
	now := time.Now().UTC()
	bids := []book.Book{{Price: 4000, Amount: 2}, {Price: 3999, Amount: 3}}
	asks := []book.Book{{Price: 4001, Amount: -4}, {Price: 4002, Amount: -5}}
	got := buildBookSnapshot("tXAUT:USD", bids, asks, 10, now)
	if !got.Valid() {
		t.Fatalf("snapshot invalid: %+v", got)
	}
	if got.AskQty != 4 || got.DepthBase <= 0 || got.DepthQuote <= 0 {
		t.Fatalf("quantities were not normalized: %+v", got)
	}
}

func TestNormalizeSymbolAcceptsBitfinexColonForm(t *testing.T) {
	if normalizeSymbol("tXAUT:USD") != normalizeSymbol("XAUTUSD") {
		t.Fatal("symbol normalization mismatch")
	}
}

func TestAdapterGuardCountsExistingOpeningOrders(t *testing.T) {
	cfg := config.Default()
	a := &Adapter{cfg: cfg, lastAccount: domain.AccountSnapshot{
		EquityUSD:  30_000,
		UpdatedAt:  time.Now().UTC(),
		OpenOrders: []domain.OpenOrder{{ID: 1, Venue: domain.VenueSpot, RemainingAmount: 5}},
	}}
	intent := domain.OrderIntent{Venue: domain.VenueSpot, Side: domain.SideBuy, Amount: 3, Quantity: 3, LimitPrice: 4000}
	if err := a.validateIntentAgainstCachedAccount(intent); err == nil {
		t.Fatal("expected 1x cap rejection including pending order")
	}
}

func TestNumericPaperFlagParsing(t *testing.T) {
	for _, value := range []any{float64(1), int64(1), "1"} {
		got, ok := numeric(value)
		if !ok || got != 1 {
			t.Fatalf("numeric(%T %v) = %v, %v", value, value, got, ok)
		}
	}
}

func TestPaperWalletCurrencyAliases(t *testing.T) {
	for _, tc := range map[string]string{
		"TESTUSD":  "TESTUSD",
		"TESTUSDT": "TESTUSDT",
		"TESTXAUT": "TESTXAUT",
	} {
		if got := normalizeCurrency(tc); got != tc {
			t.Fatalf("normalizeCurrency(%q) = %q", tc, got)
		}
	}
}
