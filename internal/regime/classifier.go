package regime

import (
	"math"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

type Classifier struct {
	market   config.MarketConfig
	strategy config.StrategyConfig
}

func New(m config.MarketConfig, s config.StrategyConfig) *Classifier {
	return &Classifier{market: m, strategy: s}
}

func (c *Classifier) Classify(now time.Time, direct domain.BookSnapshot, fair domain.FairValue, f domain.Features) (domain.Regime, string) {
	if !direct.Valid() {
		return domain.RegimeNoTrade, "direct order book invalid"
	}
	if now.Sub(direct.UpdatedAt) > c.market.MaximumBookAge.Duration {
		return domain.RegimeNoTrade, "direct order book stale"
	}
	if !fair.Valid {
		return domain.RegimeNoTrade, fair.Reason
	}
	if !f.Warm {
		return domain.RegimeNoTrade, "multi-timeframe feature warm-up incomplete"
	}
	if f.SpreadBPS > c.market.MaximumDirectSpreadBPS {
		return domain.RegimeNoTrade, "direct spread too wide"
	}
	if f.Volatility > c.market.HighVolatilityFraction {
		return domain.RegimeTransition, "realized volatility above safe trading threshold"
	}
	if f.VolatilityRatio >= c.market.TransitionVolRatio || math.Abs(f.TrendAcceleration) >= c.market.TransitionTrendAcceleration || f.BasisInstability >= c.market.TransitionBasisInstability {
		return domain.RegimeTransition, "volatility/trend/basis transition detected; mean reversion disabled"
	}
	// Trend has priority over mean reversion. A statistically stretched basis is
	// not a short/long signal when a strong directional move is underway.
	if math.Abs(f.TrendScore) >= c.strategy.TrendRegimeThreshold {
		return domain.RegimeTrend, "15m/1h/4h volatility-normalized trend detected"
	}
	if math.Abs(f.BasisZ) >= c.strategy.DislocationZThreshold && math.Abs(f.TrendScore) <= c.strategy.MeanReversionTrendVeto && fair.RouteDispersionBPS <= c.market.MaximumRouteDispersionBPS/2 {
		return domain.RegimeDislocation, "large executable-basis deviation with trend veto passed and routes agreeing"
	}
	if math.Abs(f.TrendScore) <= c.strategy.MeanReversionTrendVeto && f.BasisInstability < 1.25 {
		return domain.RegimeRange, "stable range/mean-reversion conditions"
	}
	return domain.RegimeTransition, "ambiguous transition state"
}
