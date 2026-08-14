package features

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

func TestBasisStateRoundTripAndWarmRestore(t *testing.T) {
	cfg := config.Default()
	cfg.Market.WarmupSamples = 3
	cfg.Market.BasisWindow = 5
	now := time.Now().UTC().Truncate(time.Minute)
	samples := []BasisSample{
		{Time: now.Add(-2 * time.Minute), Value: -0.002, Source: BasisSourceREST},
		{Time: now.Add(-time.Minute), Value: -0.001, Source: BasisSourceLive},
		{Time: now, Value: 0.001, Source: BasisSourceLive},
	}
	path := filepath.Join(t.TempDir(), "basis_state.json")
	if err := SaveBasisState(path, samples); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBasisState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(samples) {
		t.Fatalf("loaded %d samples, want %d", len(loaded), len(samples))
	}

	engine := New(cfg.Market)
	if got := engine.SeedBasis(loaded, now); got != 3 {
		t.Fatalf("seeded %d samples, want 3", got)
	}
	seedTrendFrames(engine, now)
	direct := domain.BookSnapshot{Bid: 100, Ask: 100.1, BidQty: 1, AskQty: 1, UpdatedAt: now}
	fair := domain.FairValue{Valid: true, Price: 100, Bid: 99.9, Ask: 100.1}
	features := engine.Update(now.Add(10*time.Second), direct, fair, nil)
	if !features.Warm {
		t.Fatalf("restored basis should be warm: %+v", features)
	}
	if features.Samples != 3 {
		t.Fatalf("same-minute update added a duplicate sample: %d", features.Samples)
	}
	got := engine.BasisSamples()
	if got[0].Source != BasisSourceREST || got[2].Source != BasisSourceLive {
		t.Fatalf("basis sources were not preserved: %+v", got)
	}
}

func TestSeedBasisDropsStaleFutureAndNonFiniteSamples(t *testing.T) {
	cfg := config.Default()
	cfg.Market.BasisWindow = 4
	cfg.Market.WarmupSamples = 2
	now := time.Now().UTC().Truncate(time.Minute)
	engine := New(cfg.Market)
	got := engine.SeedBasis([]BasisSample{
		{Time: now.Add(-5 * time.Minute), Value: 1},
		{Time: now.Add(-time.Minute), Value: 2},
		{Time: now, Value: math.Inf(1)},
		{Time: now.Add(time.Minute), Value: 3},
	}, now)
	if got != 1 {
		t.Fatalf("seeded %d samples, want only one valid sample", got)
	}
}

func seedTrendFrames(engine *Engine, now time.Time) {
	for _, item := range []struct {
		name string
		dur  time.Duration
	}{{"15m", 15 * time.Minute}, {"1h", time.Hour}, {"4h", 4 * time.Hour}} {
		candles := make([]domain.Candle, 0, 12)
		for i := 12; i > 0; i-- {
			candles = append(candles, domain.Candle{Time: now.Add(-time.Duration(i) * item.dur), Close: 100 + float64(12-i)})
		}
		engine.Seed(item.name, candles)
	}
}
