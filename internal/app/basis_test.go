package app

import (
	"math"
	"testing"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
	"xaut-paper-bot/internal/features"
)

func TestHistoricalBasisSamplesRequireAlignedClosedRoutes(t *testing.T) {
	cfg := config.Default()
	cfg.Market.BasisWindow = 10
	now := time.Now().UTC().Truncate(time.Minute).Add(30 * time.Second)
	minutes := []time.Time{
		now.Add(-3 * time.Minute).Truncate(time.Minute),
		now.Add(-2 * time.Minute).Truncate(time.Minute),
		now.Add(-time.Minute).Truncate(time.Minute),
		now.Truncate(time.Minute), // open candle: must be ignored
	}
	candles := map[string][]domain.Candle{}
	for _, at := range minutes {
		candles[cfg.Symbols.XAUTUSD] = append(candles[cfg.Symbols.XAUTUSD], domain.Candle{Time: at, Close: 100})
		candles[cfg.Symbols.XAUTUST] = append(candles[cfg.Symbols.XAUTUST], domain.Candle{Time: at, Close: 100})
		candles[cfg.Symbols.XAUTBTC] = append(candles[cfg.Symbols.XAUTBTC], domain.Candle{Time: at, Close: 0.01})
		candles[cfg.Symbols.BTCUSD] = append(candles[cfg.Symbols.BTCUSD], domain.Candle{Time: at, Close: 10_000})
	}
	// Omit the second closed minute from UST/USD, making that bucket unusable.
	for i, at := range minutes {
		if i == 1 {
			continue
		}
		candles[cfg.Symbols.USTUSD] = append(candles[cfg.Symbols.USTUSD], domain.Candle{Time: at, Close: 1})
	}

	got := historicalBasisSamples(cfg.Symbols, cfg.Market, candles, now)
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2 aligned closed minutes: %+v", len(got), got)
	}
	if !got[0].Time.Equal(minutes[0]) || !got[1].Time.Equal(minutes[2]) {
		t.Fatalf("unexpected sample timestamps: %+v", got)
	}
	for _, sample := range got {
		if math.Abs(sample.Value) > 1e-12 || sample.Source != features.BasisSourceREST {
			t.Fatalf("unexpected historical basis sample: %+v", sample)
		}
	}
}

func TestMergeBasisSamplesPrefersLiveBookValue(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Minute)
	dst := map[time.Time]features.BasisSample{}
	mergeBasisSamples(dst, []features.BasisSample{{Time: at, Value: 1, Source: features.BasisSourceLive}})
	mergeBasisSamples(dst, []features.BasisSample{{Time: at, Value: 2, Source: features.BasisSourceREST}})
	if got := dst[at]; got.Value != 1 || got.Source != features.BasisSourceLive {
		t.Fatalf("REST sample replaced exact live sample: %+v", got)
	}
}
