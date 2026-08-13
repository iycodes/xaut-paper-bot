package execution

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

type Planner struct {
	cfg     config.ExecutionConfig
	symbols config.SymbolConfig
}

func New(cfg config.ExecutionConfig, s config.SymbolConfig) *Planner {
	return &Planner{cfg: cfg, symbols: s}
}

type Input struct {
	Now             time.Time
	Account         domain.AccountSnapshot
	Target          domain.Target
	Position        domain.PositionState
	Direct          domain.BookSnapshot
	RecentVolumeUSD float64
	Urgent          bool
	BotGID          int64
}

func (p *Planner) Plan(in Input) domain.ExecutionPlan {
	if !in.Direct.Valid() {
		return domain.ExecutionPlan{Reason: "direct book invalid"}
	}
	normal := filterGID(in.Account.OpenOrders, p.cfg.GroupID)
	stops := filterGID(in.Account.OpenOrders, p.cfg.StopGroupID)
	// Never reduce/flatten/reverse while an old protective stop can also execute.
	if len(stops) > 0 && needsStopCancellation(in.Target.QuantityXAUT, in.Account) {
		ids := ids(stops)
		return domain.ExecutionPlan{CancelOrderIDs: ids, Reason: "cancel protective stop before reduction/reversal"}
	}
	if len(normal) > 0 {
		stale := []int64{}
		for _, o := range normal {
			if o.CreatedAt.IsZero() || in.Now.Sub(o.CreatedAt) > p.cfg.MaximumOrderAge.Duration || !orderSupportsTarget(o, in.Target.QuantityXAUT, in.Account) {
				stale = append(stale, o.ID)
			}
		}
		if len(stale) > 0 {
			sort.Slice(stale, func(i, j int) bool { return stale[i] < stale[j] })
			return domain.ExecutionPlan{CancelOrderIDs: stale, Reason: "cancel stale or target-inconsistent working order"}
		}
		return domain.ExecutionPlan{Reason: "working bot order already exists"}
	}

	target := in.Target.QuantityXAUT
	spot := math.Max(0, in.Account.SpotXAUT)
	margin := math.Min(0, in.Account.MarginXAUT)
	if target > p.cfg.TargetToleranceXAUT && margin < -p.cfg.TargetToleranceXAUT {
		return p.marginClose(in, math.Abs(margin), "close margin short before opening spot long")
	}
	if target < -p.cfg.TargetToleranceXAUT && spot > p.cfg.TargetToleranceXAUT {
		return p.spotSell(in, spot, "sell spot before opening margin short")
	}
	if math.Abs(target) <= p.cfg.TargetToleranceXAUT {
		if spot > p.cfg.TargetToleranceXAUT {
			return p.spotSell(in, spot, "flatten spot long")
		}
		if margin < -p.cfg.TargetToleranceXAUT {
			return p.marginClose(in, math.Abs(margin), "flatten margin short")
		}
		return domain.ExecutionPlan{Reason: "already flat"}
	}
	if target > 0 {
		delta := target - spot
		if math.Abs(delta) > p.cfg.TargetToleranceXAUT {
			if delta > 0 {
				return p.spotBuy(in, delta, "increase spot long")
			}
			return p.spotSell(in, math.Abs(delta), "reduce spot long")
		}
	}
	if target < 0 {
		desired := math.Abs(target)
		current := math.Abs(margin)
		delta := desired - current
		if math.Abs(delta) > p.cfg.TargetToleranceXAUT {
			if delta > 0 {
				return p.marginSell(in, delta, "increase margin short")
			}
			return p.marginClose(in, math.Abs(delta), "reduce margin short")
		}
	}
	return p.protective(in, stops)
}

func (p *Planner) protective(in Input, stops []domain.OpenOrder) domain.ExecutionPlan {
	if !p.cfg.ProtectiveStops || math.Abs(in.Position.QuantityXAUT) <= p.cfg.TargetToleranceXAUT || in.Position.StopPrice <= 0 {
		return domain.ExecutionPlan{Reason: "target within tolerance"}
	}
	if len(stops) > 0 {
		expected := math.Abs(in.Position.QuantityXAUT)
		for _, o := range stops {
			if math.Abs(math.Abs(o.RemainingAmount)-expected) > p.cfg.QuantityStep*2 || math.Abs(o.Price-in.Position.StopPrice) > p.cfg.PriceStep*1.5 {
				return domain.ExecutionPlan{CancelOrderIDs: ids(stops), Reason: "refresh protective stop quantity/price"}
			}
		}
		return domain.ExecutionPlan{Reason: "protective exchange stop active"}
	}
	qty := math.Abs(in.Position.QuantityXAUT)
	side := domain.SideSell
	venue := domain.VenueSpot
	closeOnly := false
	if in.Position.QuantityXAUT < 0 {
		side = domain.SideBuy
		venue = domain.VenueMargin
		closeOnly = true
	}
	qty = roundDown(qty, p.cfg.QuantityStep)
	if qty < p.cfg.MinimumXAUTQuantity {
		return domain.ExecutionPlan{Reason: "protective quantity below minimum"}
	}
	amt := qty
	if side == domain.SideSell {
		amt = -qty
	}
	intent := &domain.OrderIntent{Venue: venue, Side: side, Symbol: p.symbols.OrderPair, OrderType: "STOP", Amount: amt, Quantity: qty, LimitPrice: roundPrice(in.Position.StopPrice, p.cfg.PriceStep, side), PostOnly: false, CloseOnly: closeOnly, Protective: true, Urgency: domain.UrgencyUrgent, GID: p.cfg.StopGroupID, Reason: "exchange-side protective stop"}
	return domain.ExecutionPlan{Intent: intent, Reason: intent.Reason}
}
func needsStopCancellation(target float64, a domain.AccountSnapshot) bool {
	current := a.NetXAUT()
	if math.Abs(current) < 1e-9 {
		return true
	}
	if target == 0 {
		return true
	}
	if (target > 0) != (current > 0) {
		return true
	}
	return math.Abs(target) < math.Abs(current)-1e-9
}
func (p *Planner) spotBuy(in Input, q float64, r string) domain.ExecutionPlan {
	q = p.cap(q, in)
	if q < p.cfg.MinimumXAUTQuantity {
		return domain.ExecutionPlan{Reason: "spot buy below minimum"}
	}
	price, post, u := p.buyPrice(in)
	return p.intent(domain.VenueSpot, domain.SideBuy, q, price, in.Target.StopDistance, post, false, u, r)
}
func (p *Planner) spotSell(in Input, q float64, r string) domain.ExecutionPlan {
	q = p.cap(q, in)
	if q < p.cfg.MinimumXAUTQuantity {
		return domain.ExecutionPlan{Reason: "spot sell below minimum"}
	}
	price, post, u := p.sellPrice(in)
	return p.intent(domain.VenueSpot, domain.SideSell, q, price, 0, post, false, u, r)
}
func (p *Planner) marginSell(in Input, q float64, r string) domain.ExecutionPlan {
	q = p.cap(q, in)
	if q < p.cfg.MinimumXAUTQuantity {
		return domain.ExecutionPlan{Reason: "margin short below minimum"}
	}
	price, post, u := p.sellPrice(in)
	return p.intent(domain.VenueMargin, domain.SideSell, q, price, in.Target.StopDistance, post, false, u, r)
}
func (p *Planner) marginClose(in Input, q float64, r string) domain.ExecutionPlan {
	q = p.cap(q, in)
	if q < p.cfg.MinimumXAUTQuantity {
		return domain.ExecutionPlan{Reason: "margin close below minimum"}
	}
	price, post, u := p.buyPrice(in)
	return p.intent(domain.VenueMargin, domain.SideBuy, q, price, 0, post, true, u, r)
}
func (p *Planner) intent(v domain.Venue, s domain.Side, q, price, stop float64, post, closeOnly bool, u domain.Urgency, r string) domain.ExecutionPlan {
	q = roundDown(q, p.cfg.QuantityStep)
	if q < p.cfg.MinimumXAUTQuantity {
		return domain.ExecutionPlan{Reason: "rounded quantity below minimum"}
	}
	price = roundPrice(price, p.cfg.PriceStep, s)
	a := q
	if s == domain.SideSell {
		a = -q
	}
	in := &domain.OrderIntent{Venue: v, Side: s, Symbol: p.symbols.OrderPair, OrderType: "LIMIT", Amount: a, Quantity: q, LimitPrice: price, StopDistance: stop, PostOnly: post, CloseOnly: closeOnly, Urgency: u, GID: p.cfg.GroupID, Reason: r}
	return domain.ExecutionPlan{Intent: in, Reason: r}
}
func (p *Planner) cap(q float64, in Input) float64 {
	if q <= 0 {
		return 0
	}
	price := in.Direct.Mid()
	if price <= 0 {
		return 0
	}
	caps := []float64{q}
	if p.cfg.MaximumChildNotionalUSD > 0 {
		caps = append(caps, p.cfg.MaximumChildNotionalUSD/price)
	}
	if p.cfg.DepthParticipation > 0 && in.Direct.DepthQuote > 0 {
		caps = append(caps, in.Direct.DepthQuote*p.cfg.DepthParticipation/price)
	}
	if p.cfg.RecentVolumeParticipation > 0 && in.RecentVolumeUSD > 0 {
		caps = append(caps, in.RecentVolumeUSD*p.cfg.RecentVolumeParticipation/price)
	}
	m := caps[0]
	for _, x := range caps[1:] {
		if x < m {
			m = x
		}
	}
	return m
}
func (p *Planner) buyPrice(in Input) (float64, bool, domain.Urgency) {
	if in.Urgent {
		return in.Direct.Ask * (1 + p.cfg.UrgentSlippageBPS/10_000), false, domain.UrgencyUrgent
	}
	return in.Direct.Bid, true, domain.UrgencyPassive
}
func (p *Planner) sellPrice(in Input) (float64, bool, domain.Urgency) {
	if in.Urgent {
		return in.Direct.Bid * (1 - p.cfg.UrgentSlippageBPS/10_000), false, domain.UrgencyUrgent
	}
	return in.Direct.Ask, true, domain.UrgencyPassive
}
func filterGID(os []domain.OpenOrder, g int64) []domain.OpenOrder {
	r := []domain.OpenOrder{}
	for _, o := range os {
		if o.GID == g {
			r = append(r, o)
		}
	}
	return r
}
func ids(os []domain.OpenOrder) []int64 {
	r := make([]int64, 0, len(os))
	for _, o := range os {
		r = append(r, o.ID)
	}
	sort.Slice(r, func(i, j int) bool { return r[i] < r[j] })
	return r
}
func orderSupportsTarget(o domain.OpenOrder, target float64, a domain.AccountSnapshot) bool {
	if strings.Contains(strings.ToUpper(o.Type), "STOP") {
		return true
	}
	if target > 0 {
		if a.MarginXAUT < 0 {
			return o.Venue == domain.VenueMargin && o.RemainingAmount > 0
		}
		d := target - math.Max(0, a.SpotXAUT)
		return (d > 0 && o.Venue == domain.VenueSpot && o.RemainingAmount > 0) || (d < 0 && o.Venue == domain.VenueSpot && o.RemainingAmount < 0)
	}
	if target < 0 {
		if a.SpotXAUT > 0 {
			return o.Venue == domain.VenueSpot && o.RemainingAmount < 0
		}
		d := math.Abs(target) - math.Abs(math.Min(0, a.MarginXAUT))
		return (d > 0 && o.Venue == domain.VenueMargin && o.RemainingAmount < 0) || (d < 0 && o.Venue == domain.VenueMargin && o.RemainingAmount > 0)
	}
	return (o.Venue == domain.VenueSpot && o.RemainingAmount < 0) || (o.Venue == domain.VenueMargin && o.RemainingAmount > 0)
}
func roundDown(v, step float64) float64 {
	if step <= 0 {
		return v
	}
	return math.Floor((v+1e-12)/step) * step
}
func roundPrice(v, step float64, s domain.Side) float64 {
	if step <= 0 {
		return v
	}
	if s == domain.SideBuy {
		return math.Floor((v+1e-12)/step) * step
	}
	return math.Ceil((v-1e-12)/step) * step
}
func Describe(plan domain.ExecutionPlan) string {
	if plan.Intent != nil {
		return fmt.Sprintf("%s %s %.4f @ %.2f (%s)", plan.Intent.Venue, plan.Intent.Side, plan.Intent.Quantity, plan.Intent.LimitPrice, plan.Intent.Reason)
	}
	if len(plan.CancelOrderIDs) > 0 {
		return fmt.Sprintf("cancel %d order(s): %s", len(plan.CancelOrderIDs), plan.Reason)
	}
	return plan.Reason
}
