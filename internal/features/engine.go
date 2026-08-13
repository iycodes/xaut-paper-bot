package features

import (
	"math"
	"sort"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
	"xaut-paper-bot/internal/series"
)

type frame struct {
	dur       time.Duration
	closes    *series.Window
	returns   *series.Window
	bucket    time.Time
	current   float64
	lastClose float64
}

type Engine struct {
	cfg         config.MarketConfig
	basis       *series.Window
	basisBucket time.Time
	f15         *frame
	f1h         *frame
	f4h         *frame
	prevTrend   float64
	samples     int
}

func New(cfg config.MarketConfig) *Engine {
	return &Engine{
		cfg:   cfg,
		basis: series.New(cfg.BasisWindow),
		f15:   newFrame(15*time.Minute, 160),
		f1h:   newFrame(time.Hour, 120),
		f4h:   newFrame(4*time.Hour, 100),
	}
}
func newFrame(d time.Duration, n int) *frame {
	return &frame{dur: d, closes: series.New(n), returns: series.New(n)}
}

func (e *Engine) Seed(timeframe string, candles []domain.Candle) {
	var f *frame
	switch timeframe {
	case "15m":
		f = e.f15
	case "1h":
		f = e.f1h
	case "4h":
		f = e.f4h
	default:
		return
	}
	sort.Slice(candles, func(i, j int) bool { return candles[i].Time.Before(candles[j].Time) })
	for _, c := range candles {
		if c.Close <= 0 {
			continue
		}
		f.addClosed(c.Time, c.Close)
	}
}

func (e *Engine) Update(now time.Time, direct domain.BookSnapshot, fair domain.FairValue, trades []domain.PublicTrade) domain.Features {
	out := domain.Features{SpreadBPS: direct.SpreadBPS(), Samples: e.samples}
	if !direct.Valid() || !fair.Valid || fair.Price <= 0 {
		return out
	}
	price := direct.Mid()
	e.f15.update(now, price)
	e.f1h.update(now, price)
	e.f4h.update(now, price)

	// Basis statistics are sampled once per minute, avoiding artificial variance
	// compression when a quiet XAUT book is observed every few seconds.
	minute := now.Truncate(time.Minute)
	basis := math.Log(price / fair.Price)
	if e.basisBucket.IsZero() || minute.After(e.basisBucket) {
		e.basis.Add(basis)
		e.basisBucket = minute
		e.samples++
	}

	stdBasis := e.basis.StdDev()
	if stdBasis > 1e-12 {
		out.BasisZ = (basis - e.basis.Mean()) / stdBasis
	}
	out.BasisScore = -math.Tanh(out.BasisZ / 2)
	out.Trend15m = frameTrend(e.f15, 8)
	out.Trend1h = frameTrend(e.f1h, 12)
	out.Trend4h = frameTrend(e.f4h, 12)
	out.TrendScore = clamp(e.cfg.Trend15mWeight*out.Trend15m+e.cfg.Trend1hWeight*out.Trend1h+e.cfg.Trend4hWeight*out.Trend4h, -1, 1)
	out.TrendAcceleration = out.TrendScore - e.prevTrend
	e.prevTrend = out.TrendScore

	out.Volatility = frameVolatility1h(e.f15)
	out.VolatilityRatio = volatilityRatio(e.f15.returns.Values(), 8, 40)
	out.BasisInstability = basisInstability(e.basis.Values())
	out.DepthImbalance = depthImbalance(direct, e.cfg.MicroDepthLevels)
	out.OrderFlowScore = tradeFlow(now, trades, e.cfg.TradeFlowLookback.Duration)
	halfSpread := (direct.Ask - direct.Bid) / 2
	microNorm := 0.0
	if halfSpread > 0 {
		microNorm = clamp((direct.MicroPrice()-direct.Mid())/halfSpread, -1, 1)
	}
	// Actual executed flow receives the largest weight; displayed liquidity alone
	// cannot dominate the signal.
	out.MicroScore = clamp(.25*out.DepthImbalance+.35*out.OrderFlowScore+.20*microNorm+.20*flowPersistence(trades), -1, 1)
	out.Samples = e.samples
	out.Warm = e.basis.Len() >= e.cfg.WarmupSamples && e.f15.closes.Len() >= 12 && e.f1h.closes.Len() >= 12 && e.f4h.closes.Len() >= 12
	return out
}

func (f *frame) addClosed(at time.Time, close float64) {
	if close <= 0 {
		return
	}
	if f.lastClose > 0 {
		f.returns.Add(math.Log(close / f.lastClose))
	}
	f.closes.Add(close)
	f.lastClose = close
	f.bucket = at.Truncate(f.dur)
	f.current = close
}
func (f *frame) update(now time.Time, price float64) {
	b := now.Truncate(f.dur)
	if f.bucket.IsZero() {
		f.bucket = b
		f.current = price
		return
	}
	if b.After(f.bucket) {
		if f.current > 0 {
			f.addClosed(f.bucket, f.current)
		}
		f.bucket = b
		f.current = price
		return
	}
	f.current = price
}
func frameTrend(f *frame, lookback int) float64 {
	vals := f.closes.Values()
	if f.current > 0 {
		vals = append(vals, f.current)
	}
	if len(vals) < 3 {
		return 0
	}
	if lookback >= len(vals) {
		lookback = len(vals) - 1
	}
	start := vals[len(vals)-1-lookback]
	last := vals[len(vals)-1]
	if start <= 0 || last <= 0 {
		return 0
	}
	rs := f.returns.Values()
	sigma := std(rs)
	if sigma < 1e-6 {
		sigma = 1e-6
	}
	normalized := math.Log(last/start) / (sigma * math.Sqrt(float64(lookback)))
	return math.Tanh(normalized / 2)
}
func frameVolatility1h(f15 *frame) float64 {
	v := std(f15.returns.Values())
	if v <= 0 {
		return 0
	}
	return v * math.Sqrt(4)
}
func volatilityRatio(v []float64, recentN, longN int) float64 {
	if len(v) < recentN+2 {
		return 1
	}
	if longN > len(v) {
		longN = len(v)
	}
	r := std(v[len(v)-recentN:])
	l := std(v[len(v)-longN:])
	if l < 1e-9 {
		return 1
	}
	return r / l
}
func basisInstability(v []float64) float64 {
	if len(v) < 20 {
		return 0
	}
	n := len(v) / 4
	if n < 5 {
		n = 5
	}
	recent := std(v[len(v)-n:])
	all := std(v)
	if all < 1e-9 {
		return 0
	}
	return recent / all
}
func depthImbalance(b domain.BookSnapshot, levels int) float64 {
	if levels < 1 {
		levels = 1
	}
	var bid, ask float64
	if len(b.Bids) > 0 && len(b.Asks) > 0 {
		for i := 0; i < levels && i < len(b.Bids); i++ {
			bid += math.Abs(b.Bids[i].Amount) / float64(i+1)
		}
		for i := 0; i < levels && i < len(b.Asks); i++ {
			ask += math.Abs(b.Asks[i].Amount) / float64(i+1)
		}
	} else {
		bid = b.BidQty
		ask = b.AskQty
	}
	return safeDiv(bid-ask, bid+ask)
}
func tradeFlow(now time.Time, trades []domain.PublicTrade, lookback time.Duration) float64 {
	var buy, sell float64
	for _, t := range trades {
		if now.Sub(t.Time) > lookback || t.Price <= 0 {
			continue
		}
		v := math.Abs(t.Amount) * t.Price
		if t.Amount > 0 {
			buy += v
		} else if t.Amount < 0 {
			sell += v
		}
	}
	return safeDiv(buy-sell, buy+sell)
}
func flowPersistence(trades []domain.PublicTrade) float64 {
	if len(trades) < 4 {
		return 0
	}
	n := len(trades)
	if n > 20 {
		trades = trades[n-20:]
	}
	var signed, total float64
	for _, t := range trades {
		w := math.Abs(t.Amount)
		total += w
		if t.Amount > 0 {
			signed += w
		} else {
			signed -= w
		}
	}
	return safeDiv(signed, total)
}
func std(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	var m float64
	for _, x := range v {
		m += x
	}
	m /= float64(len(v))
	var ss float64
	for _, x := range v {
		d := x - m
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(v)-1))
}
func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
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
