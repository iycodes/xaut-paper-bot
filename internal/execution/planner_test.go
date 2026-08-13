package execution

import (
	"testing"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

func testBook() domain.BookSnapshot {
	return domain.BookSnapshot{Bid: 3999, Ask: 4001, BidQty: 10, AskQty: 10, UpdatedAt: time.Now().UTC()}
}

func TestClosesMarginShortBeforeSpotLong(t *testing.T) {
	cfg := config.Default()
	p := New(cfg.Execution, cfg.Symbols)
	plan := p.Plan(Input{Now: time.Now(), Account: domain.AccountSnapshot{MarginXAUT: -2}, Target: domain.Target{QuantityXAUT: 1}, Direct: testBook(), BotGID: cfg.Execution.GroupID})
	if plan.Intent == nil || plan.Intent.Venue != domain.VenueMargin || plan.Intent.Side != domain.SideBuy || !plan.Intent.CloseOnly {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestSellsSpotBeforeOpeningShort(t *testing.T) {
	cfg := config.Default()
	p := New(cfg.Execution, cfg.Symbols)
	plan := p.Plan(Input{Now: time.Now(), Account: domain.AccountSnapshot{SpotXAUT: 2}, Target: domain.Target{QuantityXAUT: -1}, Direct: testBook(), BotGID: cfg.Execution.GroupID})
	if plan.Intent == nil || plan.Intent.Venue != domain.VenueSpot || plan.Intent.Side != domain.SideSell {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestOpensMarginShortWithNegativeAmount(t *testing.T) {
	cfg := config.Default()
	p := New(cfg.Execution, cfg.Symbols)
	plan := p.Plan(Input{Now: time.Now(), Account: domain.AccountSnapshot{}, Target: domain.Target{QuantityXAUT: -1}, Direct: testBook(), BotGID: cfg.Execution.GroupID})
	if plan.Intent == nil || plan.Intent.Venue != domain.VenueMargin || plan.Intent.Amount >= 0 || plan.Intent.CloseOnly {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestUrgentCloseUsesMarketableBoundedLimit(t *testing.T) {
	cfg := config.Default()
	p := New(cfg.Execution, cfg.Symbols)
	plan := p.Plan(Input{Now: time.Now(), Account: domain.AccountSnapshot{MarginXAUT: -1}, Target: domain.Target{}, Direct: testBook(), Urgent: true, BotGID: cfg.Execution.GroupID})
	if plan.Intent == nil || plan.Intent.PostOnly || plan.Intent.LimitPrice <= testBook().Ask {
		t.Fatalf("unexpected urgent plan: %+v", plan)
	}
}

func TestCancelsOpeningOrderWhenTargetNowRequiresReduction(t *testing.T) {
	cfg := config.Default()
	p := New(cfg.Execution, cfg.Symbols)
	now := time.Now().UTC()
	account := domain.AccountSnapshot{
		SpotXAUT: 2,
		OpenOrders: []domain.OpenOrder{{
			ID: 10, GID: cfg.Execution.GroupID, Venue: domain.VenueSpot,
			RemainingAmount: 1, CreatedAt: now,
		}},
	}
	plan := p.Plan(Input{Now: now, Account: account, Target: domain.Target{QuantityXAUT: 1}, Direct: testBook(), BotGID: cfg.Execution.GroupID})
	if len(plan.CancelOrderIDs) != 1 || plan.CancelOrderIDs[0] != 10 {
		t.Fatalf("expected inconsistent buy cancellation: %+v", plan)
	}
}
