package fairvalue

import (
	"fmt"
	"math"
	"sort"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

type Engine struct {
	symbols        config.SymbolConfig
	market         config.MarketConfig
	gold           config.GoldConfig
	goldBasis      float64
	goldBasisReady bool
}

func New(symbols config.SymbolConfig, market config.MarketConfig, gold config.GoldConfig) *Engine {
	return &Engine{symbols: symbols, market: market, gold: gold}
}

// Estimate creates side-specific executable fair values. Synthetic bid/ask use
// the corresponding executable side of every leg, never route midpoints.
func (e *Engine) Estimate(books map[string]domain.BookSnapshot, gold domain.GoldReference, now time.Time) domain.FairValue {
	result := domain.FairValue{UpdatedAt: now}
	routes := make([]domain.RouteQuote, 0, 3)
	if r, ok := route("XAUT/UST × UST/USD", books[e.symbols.XAUTUST], books[e.symbols.USTUSD], now, e.market.MaximumBookAge.Duration); ok {
		routes = append(routes, r)
	}
	if r, ok := route("XAUT/BTC × BTC/USD", books[e.symbols.XAUTBTC], books[e.symbols.BTCUSD], now, e.market.MaximumBookAge.Duration); ok {
		routes = append(routes, r)
	}
	if len(routes) < 2 {
		result.Reason = fmt.Sprintf("need two fresh executable fair-value routes; got %d", len(routes))
		result.Routes = routes
		return result
	}

	// Weighted medians are resistant to one distorted route. With two routes
	// this behaves as a spread-aware weighted blend.
	result.Bid = weightedMeanSide(routes, true)
	result.Ask = weightedMeanSide(routes, false)
	if result.Ask < result.Bid {
		result.Bid, result.Ask = result.Ask, result.Bid
	}
	result.Price = (result.Bid + result.Ask) / 2

	if e.gold.Enabled && gold.Valid && gold.Price > 0 && now.Sub(gold.UpdatedAt) <= e.gold.MaximumAge.Duration {
		direct := books[e.symbols.XAUTUSD]
		if direct.Valid() {
			observed := math.Log(direct.Mid() / gold.Price)
			if !e.goldBasisReady {
				e.goldBasis = observed
				e.goldBasisReady = true
			} else {
				a := clamp(e.gold.BasisEWMAAlpha, 0.001, 1)
				e.goldBasis = a*observed + (1-a)*e.goldBasis
			}
			anchored := gold.Price * math.Exp(e.goldBasis)
			dev := math.Abs(anchored-result.Price) / result.Price * 10_000
			if dev <= e.gold.MaximumDeviationBPS {
				result.GoldAnchoredPrice = anchored
				// Gold is an anchor, not a tradable route. Blend modestly so crypto-route
				// execution still dominates immediate pricing.
				result.Price = .80*result.Price + .20*anchored
			}
		}
	}

	result.Routes = routes
	minMid, maxMid := routes[0].Mid, routes[0].Mid
	var spreadWeighted, totalWeight float64
	for _, r := range routes {
		minMid = math.Min(minMid, r.Mid)
		maxMid = math.Max(maxMid, r.Mid)
		spreadWeighted += r.SpreadBPS * r.Weight
		totalWeight += r.Weight
	}
	if result.Price <= 0 || totalWeight <= 0 {
		result.Reason = "computed fair value invalid"
		return result
	}
	result.RouteDispersionBPS = (maxMid - minMid) / result.Price * 10_000
	avgSpread := spreadWeighted / totalWeight
	result.UncertaintyBPS = e.market.FairValueModelBufferBPS + avgSpread/2 + result.RouteDispersionBPS
	maxU := math.Max(1, e.market.MaximumRouteDispersionBPS*2)
	result.Confidence = clamp(1-result.UncertaintyBPS/maxU, 0, 1)
	if result.RouteDispersionBPS > e.market.MaximumRouteDispersionBPS {
		result.Reason = fmt.Sprintf("route dispersion %.2f bps exceeds %.2f", result.RouteDispersionBPS, e.market.MaximumRouteDispersionBPS)
		return result
	}
	result.Valid = result.Bid > 0 && result.Ask >= result.Bid && result.Price > 0
	if !result.Valid {
		result.Reason = "computed executable fair value is not positive"
	}
	return result
}

func route(name string, base, quote domain.BookSnapshot, now time.Time, maxAge time.Duration) (domain.RouteQuote, bool) {
	if !base.Valid() || !quote.Valid() || now.Sub(base.UpdatedAt) > maxAge || now.Sub(quote.UpdatedAt) > maxAge {
		return domain.RouteQuote{}, false
	}
	bid := base.Bid * quote.Bid
	ask := base.Ask * quote.Ask
	if bid <= 0 || ask <= bid {
		return domain.RouteQuote{}, false
	}
	mid := (bid + ask) / 2
	spread := (ask - bid) / mid * 10_000
	return domain.RouteQuote{Name: name, Bid: bid, Ask: ask, Mid: mid, SpreadBPS: spread, Weight: routeWeight(spread)}, true
}
func weightedMeanSide(routes []domain.RouteQuote, bid bool) float64 {
	rs := append([]domain.RouteQuote(nil), routes...)
	sort.Slice(rs, func(i, j int) bool { return rs[i].Weight > rs[j].Weight })
	var n, d float64
	for _, r := range rs {
		p := r.Ask
		if bid {
			p = r.Bid
		}
		n += p * r.Weight
		d += r.Weight
	}
	if d == 0 {
		return 0
	}
	return n / d
}
func routeWeight(spread float64) float64 { return 1 / math.Max(1, 1+spread) }
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
