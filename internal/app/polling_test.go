package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

type fakePublicData struct {
	tradeCalls   int
	fundingCalls int
	tradeErr     error
	fundingErr   error
}

func (f *fakePublicData) PublicTrades(context.Context, string, time.Time, int) ([]domain.PublicTrade, error) {
	f.tradeCalls++
	if f.tradeErr != nil {
		return nil, f.tradeErr
	}
	return []domain.PublicTrade{{ID: int64(f.tradeCalls), Amount: 1, Price: 100}}, nil
}

func (f *fakePublicData) Funding(context.Context) (domain.FundingSnapshot, error) {
	f.fundingCalls++
	if f.fundingErr != nil {
		return domain.FundingSnapshot{}, f.fundingErr
	}
	return domain.FundingSnapshot{Valid: true, DailyRate: 0.0003, UpdatedAt: time.Now().UTC()}, nil
}

func TestRESTPollingStaysBelowPublicTradesLimit(t *testing.T) {
	cfg := config.Default()
	fake := &fakePublicData{}
	app := App{cfg: cfg, publicData: fake, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	start := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		now := start.Add(time.Duration(i) * 5 * time.Second)
		trades := app.tradesForTick(context.Background(), now)
		funding := app.fundingForTick(context.Background(), now)
		if len(trades) == 0 || !funding.Valid {
			t.Fatal("successful responses were not cached")
		}
	}
	if fake.tradeCalls != 6 {
		t.Fatalf("market trade calls in 60 seconds = %d, want 6", fake.tradeCalls)
	}
	if fake.fundingCalls != 1 {
		t.Fatalf("funding calls in 60 seconds = %d, want 1", fake.fundingCalls)
	}
}

func TestRateLimitFailureBacksOffWithoutRepeatedCalls(t *testing.T) {
	cfg := config.Default()
	fake := &fakePublicData{tradeErr: errors.New("429 ratelimit: error (11010)")}
	app := App{cfg: cfg, publicData: fake, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	start := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	app.tradesForTick(context.Background(), start)
	app.tradesForTick(context.Background(), start.Add(55*time.Second))
	if fake.tradeCalls != 1 {
		t.Fatalf("called during first rate-limit backoff: %d", fake.tradeCalls)
	}
	if app.marketPoll.backoff != time.Minute {
		t.Fatalf("first rate-limit backoff = %s, want 1m", app.marketPoll.backoff)
	}

	app.tradesForTick(context.Background(), start.Add(time.Minute))
	app.tradesForTick(context.Background(), start.Add(2*time.Minute))
	if fake.tradeCalls != 2 {
		t.Fatalf("called during doubled rate-limit backoff: %d", fake.tradeCalls)
	}
	if app.marketPoll.backoff != 2*time.Minute {
		t.Fatalf("second rate-limit backoff = %s, want 2m", app.marketPoll.backoff)
	}
}

func TestRateLimitPausesBothTradeHistoryStreams(t *testing.T) {
	cfg := config.Default()
	fake := &fakePublicData{tradeErr: errors.New("429 ratelimit: error (11010)")}
	app := App{cfg: cfg, publicData: fake, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	app.tradesForTick(context.Background(), now)
	app.fundingForTick(context.Background(), now)
	if fake.tradeCalls != 1 || fake.fundingCalls != 0 {
		t.Fatalf("shared endpoint was not paused: trade calls=%d funding calls=%d", fake.tradeCalls, fake.fundingCalls)
	}
}

func TestRateLimitErrorDetection(t *testing.T) {
	for _, message := range []string{"HTTP 429", "ratelimit", "rate limit exceeded", "error (11010)"} {
		if !isRateLimitError(message) {
			t.Fatalf("did not recognize %q as a rate-limit error", message)
		}
	}
	if isRateLimitError("connection reset") {
		t.Fatal("ordinary network error classified as rate limit")
	}
}
