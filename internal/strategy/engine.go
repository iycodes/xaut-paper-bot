package strategy

import (
	"fmt"
	"math"
	"sync"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

type Engine struct {
	cfg            config.StrategyConfig
	funding        config.FundingConfig
	mu             sync.Mutex
	blockedLongAt  time.Time
	blockedShortAt time.Time
}

func New(cfg config.StrategyConfig, f config.FundingConfig) *Engine {
	return &Engine{cfg: cfg, funding: f}
}

type Input struct {
	Now             time.Time
	Regime          domain.Regime
	RegimeReason    string
	Features        domain.Features
	Fair            domain.FairValue
	Direct          domain.BookSnapshot
	Funding         domain.FundingSnapshot
	CurrentExposure float64
}

// MarkStopped blocks immediate re-entry in the stopped direction. The block is
// released only after both a minimum cooldown and a thesis reset in Signal.
func (e *Engine) MarkStopped(direction float64, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if direction > 0 {
		e.blockedLongAt = now
	} else if direction < 0 {
		e.blockedShortAt = now
	}
}

func (e *Engine) Signal(in Input) domain.Signal {
	if in.Regime == domain.RegimeNoTrade || in.Regime == domain.RegimeTransition {
		return domain.Signal{Regime: in.Regime, DesiredExposure: in.CurrentExposure, NoNewEntries: true, Confidence: in.Fair.Confidence, Reason: in.RegimeReason}
	}
	weights := e.weights(in.Regime)
	raw := weights.Trend*in.Features.TrendScore + weights.Basis*in.Features.BasisScore + weights.Micro*in.Features.MicroScore
	confidence := clamp(in.Fair.Confidence*(.55+.45*math.Abs(raw)), 0, 1)
	result := domain.Signal{Score: raw, Confidence: confidence, Regime: in.Regime, Reason: fmt.Sprintf("%s; weighted score %.3f", in.RegimeReason, raw), ComponentSummary: []string{
		fmt.Sprintf("trend %.3f [15m %.3f, 1h %.3f, 4h %.3f]", in.Features.TrendScore, in.Features.Trend15m, in.Features.Trend1h, in.Features.Trend4h),
		fmt.Sprintf("basis %.3f (z %.2f)", in.Features.BasisScore, in.Features.BasisZ),
		fmt.Sprintf("micro %.3f (flow %.3f)", in.Features.MicroScore, in.Features.OrderFlowScore),
	}}

	// Regime-specific exits: mean reversion exits when the basis closes; trend
	// positions exit only when the directional thesis deteriorates.
	if in.CurrentExposure > 0 {
		if (in.Regime == domain.RegimeRange || in.Regime == domain.RegimeDislocation) && math.Abs(in.Features.BasisZ) <= e.cfg.MeanReversionExitZ {
			result.DesiredExposure = 0
			result.Reason += "; mean-reversion basis normalized"
			return result
		}
		if in.Regime == domain.RegimeTrend && in.Features.TrendScore <= e.cfg.TrendExitThreshold {
			result.DesiredExposure = 0
			result.Reason += "; bullish trend thesis deteriorated"
			return result
		}
	}
	if in.CurrentExposure < 0 {
		if (in.Regime == domain.RegimeRange || in.Regime == domain.RegimeDislocation) && math.Abs(in.Features.BasisZ) <= e.cfg.MeanReversionExitZ {
			result.DesiredExposure = 0
			result.Reason += "; mean-reversion basis normalized"
			return result
		}
		if in.Regime == domain.RegimeTrend && in.Features.TrendScore >= -e.cfg.TrendExitThreshold {
			result.DesiredExposure = 0
			result.Reason += "; bearish trend thesis deteriorated"
			return result
		}
	}
	if confidence < e.cfg.MinimumConfidence {
		result.DesiredExposure = in.CurrentExposure
		result.NoNewEntries = true
		result.Reason += "; confidence below minimum"
		return result
	}

	longEdge, shortEdge := e.executableEdges(in)
	trendEdge := math.Abs(in.Features.TrendScore) * math.Max(in.Features.Volatility, 0.0001) * 10_000 * 1.5
	if in.Regime == domain.RegimeTrend {
		if raw > 0 {
			longEdge = math.Max(longEdge, trendEdge)
		} else if raw < 0 {
			shortEdge = math.Max(shortEdge, trendEdge)
		}
	}
	fundingRate := e.funding.FallbackDailyRate
	if in.Funding.Valid && in.Now.Sub(in.Funding.UpdatedAt) <= e.funding.MaximumAge.Duration {
		fundingRate = math.Max(0, in.Funding.DailyRate)
	}
	fundingBPS := fundingRate * (e.funding.ExpectedHoldingHours / 24) * 10_000
	shortNetEdge := shortEdge - fundingBPS - e.cfg.ShortExtraBufferBPS - in.Fair.UncertaintyBPS
	longNetEdge := longEdge - in.Fair.UncertaintyBPS
	result.EstimatedFundingUSD = 0

	if raw >= e.cfg.LongEntryThreshold {
		if e.reentryBlocked(1, in) {
			result.DesiredExposure = 0
			result.NoNewEntries = true
			result.Reason += "; long thesis-reset/reentry block active"
			return result
		}
		if longNetEdge < e.cfg.MinimumExpectedEdgeBPS {
			result.DesiredExposure = 0
			result.Reason += fmt.Sprintf("; long net edge %.1f bps below %.1f", longNetEdge, e.cfg.MinimumExpectedEdgeBPS)
			return result
		}
		strength := normalize(raw, e.cfg.LongEntryThreshold, 1)
		result.DesiredExposure = e.cfg.LongNormalCap * strength * confidence
		result.ExpectedEdgeBPS = longNetEdge
		result.ExpectedMoveFraction = longEdge / 10_000
		return result
	}
	if raw <= -e.cfg.ShortEntryThreshold {
		if e.reentryBlocked(-1, in) {
			result.DesiredExposure = 0
			result.NoNewEntries = true
			result.Reason += "; short thesis-reset/reentry block active"
			return result
		}
		if !in.Funding.Valid || in.Now.Sub(in.Funding.UpdatedAt) > e.funding.MaximumAge.Duration {
			result.DesiredExposure = 0
			result.NoNewEntries = true
			result.Reason += "; short blocked because funding data is stale/unavailable"
			return result
		}
		grossEdge := math.Max(shortEdge, 0.000001)
		if fundingBPS > grossEdge*e.funding.MaximumEdgeShare {
			result.DesiredExposure = 0
			result.Reason += fmt.Sprintf("; funding %.1f bps exceeds %.0f%% of gross edge %.1f", fundingBPS, e.funding.MaximumEdgeShare*100, grossEdge)
			return result
		}
		if shortNetEdge < e.cfg.MinimumExpectedEdgeBPS {
			result.DesiredExposure = 0
			result.Reason += fmt.Sprintf("; short net edge %.1f bps below %.1f", shortNetEdge, e.cfg.MinimumExpectedEdgeBPS)
			return result
		}
		strength := normalize(-raw, e.cfg.ShortEntryThreshold, 1)
		cap := e.cfg.ShortNormalCap
		if confidence >= e.cfg.HighConfidenceThreshold {
			cap = e.cfg.HighConfidenceShortCap
		}
		result.DesiredExposure = -cap * strength * confidence
		result.ExpectedEdgeBPS = shortNetEdge
		result.ExpectedMoveFraction = shortEdge / 10_000
		return result
	}
	// Hysteresis for existing positions.
	if in.CurrentExposure != 0 {
		result.DesiredExposure = in.CurrentExposure
		result.Reason += "; hold within entry/exit hysteresis"
		return result
	}
	result.DesiredExposure = 0
	result.Reason += "; inside entry dead-band"
	return result
}

func (e *Engine) executableEdges(in Input) (float64, float64) {
	if !in.Direct.Valid() || !in.Fair.Valid {
		return 0, 0
	}
	long := 0.0
	if in.Fair.Bid > in.Direct.Ask {
		long = (in.Fair.Bid/in.Direct.Ask - 1) * 10_000
	}
	short := 0.0
	if in.Direct.Bid > in.Fair.Ask {
		short = (in.Direct.Bid/in.Fair.Ask - 1) * 10_000
	}
	return long, short
}
func (e *Engine) reentryBlocked(direction float64, in Input) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	var at *time.Time
	if direction > 0 {
		at = &e.blockedLongAt
	} else {
		at = &e.blockedShortAt
	}
	if at.IsZero() {
		return false
	}
	if in.Now.Sub(*at) < e.cfg.ReentryCooldown.Duration {
		return true
	}
	reset := math.Abs(in.Features.BasisZ) <= e.cfg.ThesisResetBasisZ && math.Abs(in.Features.TrendScore) <= e.cfg.ThesisResetTrend
	if reset {
		*at = time.Time{}
		return false
	}
	return true
}
func (e *Engine) weights(r domain.Regime) config.Weights {
	switch r {
	case domain.RegimeTrend:
		return e.cfg.TrendWeights
	case domain.RegimeDislocation:
		return e.cfg.DislocationWeights
	default:
		return e.cfg.RangeWeights
	}
}
func normalize(v, threshold, max float64) float64 {
	if v <= threshold {
		return 0
	}
	if max <= threshold {
		return 1
	}
	return clamp((v-threshold)/(max-threshold), 0, 1)
}
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
