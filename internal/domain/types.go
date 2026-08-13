package domain

import "time"

type Venue string

const (
	VenueSpot   Venue = "spot"
	VenueMargin Venue = "margin"
)

type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

type Regime string

const (
	RegimeNoTrade     Regime = "no_trade"
	RegimeTransition  Regime = "transition"
	RegimeRange       Regime = "range"
	RegimeTrend       Regime = "trend"
	RegimeDislocation Regime = "dislocation"
)

type Urgency string

const (
	UrgencyPassive Urgency = "passive"
	UrgencyUrgent  Urgency = "urgent"
)

type BookLevel struct {
	Price  float64 `json:"price"`
	Amount float64 `json:"amount"`
}

type BookSnapshot struct {
	Symbol     string      `json:"symbol"`
	Bid        float64     `json:"bid"`
	Ask        float64     `json:"ask"`
	BidQty     float64     `json:"bid_qty"`
	AskQty     float64     `json:"ask_qty"`
	DepthBase  float64     `json:"depth_base"`
	DepthQuote float64     `json:"depth_quote"`
	Bids       []BookLevel `json:"bids,omitempty"`
	Asks       []BookLevel `json:"asks,omitempty"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

func (b BookSnapshot) Valid() bool {
	return b.Bid > 0 && b.Ask > b.Bid && b.BidQty > 0 && b.AskQty > 0 && !b.UpdatedAt.IsZero()
}
func (b BookSnapshot) Mid() float64 {
	if !b.Valid() {
		return 0
	}
	return (b.Bid + b.Ask) / 2
}
func (b BookSnapshot) SpreadBPS() float64 {
	m := b.Mid()
	if m <= 0 {
		return 0
	}
	return (b.Ask - b.Bid) / m * 10_000
}
func (b BookSnapshot) MicroPrice() float64 {
	denom := b.BidQty + b.AskQty
	if denom <= 0 {
		return b.Mid()
	}
	return (b.Ask*b.BidQty + b.Bid*b.AskQty) / denom
}

type PublicTrade struct {
	ID     int64     `json:"id"`
	Time   time.Time `json:"time"`
	Amount float64   `json:"amount"` // positive aggressor buy, negative aggressor sell
	Price  float64   `json:"price"`
}

type Candle struct {
	Time   time.Time `json:"time"`
	Open   float64   `json:"open"`
	Close  float64   `json:"close"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Volume float64   `json:"volume"`
}

type RouteQuote struct {
	Name      string  `json:"name"`
	Bid       float64 `json:"bid"`
	Ask       float64 `json:"ask"`
	Mid       float64 `json:"mid"`
	SpreadBPS float64 `json:"spread_bps"`
	Weight    float64 `json:"weight"`
}

type GoldReference struct {
	Price     float64   `json:"price"`
	UpdatedAt time.Time `json:"updated_at"`
	Valid     bool      `json:"valid"`
	Source    string    `json:"source,omitempty"`
}

type FairValue struct {
	Bid                float64      `json:"bid"`
	Ask                float64      `json:"ask"`
	Price              float64      `json:"price"`
	GoldAnchoredPrice  float64      `json:"gold_anchored_price,omitempty"`
	UncertaintyBPS     float64      `json:"uncertainty_bps"`
	RouteDispersionBPS float64      `json:"route_dispersion_bps"`
	Confidence         float64      `json:"confidence"`
	Routes             []RouteQuote `json:"routes"`
	Valid              bool         `json:"valid"`
	Reason             string       `json:"reason,omitempty"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

type Features struct {
	BasisZ            float64 `json:"basis_z"`
	BasisScore        float64 `json:"basis_score"`
	Trend15m          float64 `json:"trend_15m"`
	Trend1h           float64 `json:"trend_1h"`
	Trend4h           float64 `json:"trend_4h"`
	TrendScore        float64 `json:"trend_score"`
	TrendAcceleration float64 `json:"trend_acceleration"`
	MicroScore        float64 `json:"micro_score"`
	OrderFlowScore    float64 `json:"order_flow_score"`
	DepthImbalance    float64 `json:"depth_imbalance"`
	Volatility        float64 `json:"volatility"` // fractional one-hour realized vol estimate
	VolatilityRatio   float64 `json:"volatility_ratio"`
	BasisInstability  float64 `json:"basis_instability"`
	SpreadBPS         float64 `json:"spread_bps"`
	Samples           int     `json:"samples"`
	Warm              bool    `json:"warm"`
}

type FundingSnapshot struct {
	Symbol          string    `json:"symbol"`
	DailyRate       float64   `json:"daily_rate"` // decimal daily rate, e.g. 0.0003 = 0.03%/day
	AmountAvailable float64   `json:"amount_available,omitempty"`
	Source          string    `json:"source"`
	UpdatedAt       time.Time `json:"updated_at"`
	Valid           bool      `json:"valid"`
	Reason          string    `json:"reason,omitempty"`
}

type Signal struct {
	Score                float64  `json:"score"`
	Confidence           float64  `json:"confidence"`
	ExpectedMoveFraction float64  `json:"expected_move_fraction"`
	ExpectedEdgeBPS      float64  `json:"expected_edge_bps"`
	ExpectedEdgeUSD      float64  `json:"expected_edge_usd"`
	EstimatedFundingUSD  float64  `json:"estimated_funding_usd"`
	DesiredExposure      float64  `json:"desired_exposure"`
	Regime               Regime   `json:"regime"`
	NoNewEntries         bool     `json:"no_new_entries"`
	Reason               string   `json:"reason"`
	ComponentSummary     []string `json:"component_summary,omitempty"`
}

type Fill struct {
	ID          int64     `json:"id"`
	OrderID     int64     `json:"order_id"`
	CID         int64     `json:"cid"`
	Symbol      string    `json:"symbol"`
	Time        time.Time `json:"time"`
	Amount      float64   `json:"amount"`
	Price       float64   `json:"price"`
	OrderType   string    `json:"order_type"`
	Maker       bool      `json:"maker"`
	Fee         float64   `json:"fee"`
	FeeCurrency string    `json:"fee_currency"`
}

type OpenOrder struct {
	ID              int64     `json:"id"`
	GID             int64     `json:"gid"`
	CID             int64     `json:"cid"`
	Venue           Venue     `json:"venue"`
	Symbol          string    `json:"symbol"`
	Type            string    `json:"type"`
	RemainingAmount float64   `json:"remaining_amount"`
	Price           float64   `json:"price"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

type AccountSnapshot struct {
	EquityUSD        float64     `json:"equity_usd"`
	QuoteUSD         float64     `json:"quote_usd"`
	SpotXAUT         float64     `json:"spot_xaut"`
	MarginXAUT       float64     `json:"margin_xaut"`
	MarginBasePrice  float64     `json:"margin_base_price"`
	MarginPnLUSD     float64     `json:"margin_pnl_usd"`
	FundingCostUSD   float64     `json:"funding_cost_usd"`
	LiquidationPrice float64     `json:"liquidation_price"`
	OpenOrders       []OpenOrder `json:"open_orders"`
	Paper            bool        `json:"paper"`
	Synthetic        bool        `json:"synthetic"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

func (a AccountSnapshot) NetXAUT() float64 { return a.SpotXAUT + a.MarginXAUT }

type Target struct {
	Exposure          float64 `json:"exposure"`
	NotionalUSD       float64 `json:"notional_usd"`
	QuantityXAUT      float64 `json:"quantity_xaut"`
	StopDistance      float64 `json:"stop_distance"`
	StopPrice         float64 `json:"stop_price,omitempty"`
	RiskBudgetUSD     float64 `json:"risk_budget_usd"`
	ActualStopRiskUSD float64 `json:"actual_stop_risk_usd"`
	Reason            string  `json:"reason"`
}

type RiskDecision struct {
	Allowed         bool    `json:"allowed"`
	Halt            bool    `json:"halt"`
	Flatten         bool    `json:"flatten"`
	Throttle        float64 `json:"throttle"`
	Reason          string  `json:"reason"`
	EffectiveEquity float64 `json:"effective_equity"`
	DrawdownEquity  float64 `json:"drawdown_equity"`
	Target          Target  `json:"target"`
}

type OrderIntent struct {
	Venue        Venue   `json:"venue"`
	Side         Side    `json:"side"`
	Symbol       string  `json:"symbol"`
	OrderType    string  `json:"order_type,omitempty"` // LIMIT/STOP; adapter adds EXCHANGE prefix for spot
	Amount       float64 `json:"amount"`
	Quantity     float64 `json:"quantity"`
	LimitPrice   float64 `json:"limit_price"`
	StopDistance float64 `json:"stop_distance"`
	PostOnly     bool    `json:"post_only"`
	CloseOnly    bool    `json:"close_only"`
	Protective   bool    `json:"protective"`
	Urgency      Urgency `json:"urgency"`
	GID          int64   `json:"gid"`
	CID          int64   `json:"cid"`
	Reason       string  `json:"reason"`
}

type PositionState struct {
	QuantityXAUT        float64   `json:"quantity_xaut"`
	AverageEntry        float64   `json:"average_entry"`
	InitialStopFraction float64   `json:"initial_stop_fraction"`
	StopPrice           float64   `json:"stop_price"`
	ExchangeStopOrderID int64     `json:"exchange_stop_order_id,omitempty"`
	BestPrice           float64   `json:"best_price"`
	PendingStopFraction float64   `json:"pending_stop_fraction"`
	OpenedAt            time.Time `json:"opened_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	EntryBasisZ         float64   `json:"entry_basis_z,omitempty"`
	EntryTrendScore     float64   `json:"entry_trend_score,omitempty"`
	EntryRegime         Regime    `json:"entry_regime,omitempty"`
}

type ExecutionPlan struct {
	CancelOrderIDs   []int64      `json:"cancel_order_ids,omitempty"`
	Intent           *OrderIntent `json:"intent,omitempty"`
	ProtectiveIntent *OrderIntent `json:"protective_intent,omitempty"`
	Reason           string       `json:"reason"`
}

type TradeRecord struct {
	ID             string    `json:"id"`
	Direction      string    `json:"direction"`
	Regime         Regime    `json:"regime"`
	EntryTime      time.Time `json:"entry_time"`
	ExitTime       time.Time `json:"exit_time"`
	EntryVWAP      float64   `json:"entry_vwap"`
	ExitVWAP       float64   `json:"exit_vwap"`
	Quantity       float64   `json:"quantity"`
	EntryBasisZ    float64   `json:"entry_basis_z"`
	ExitBasisZ     float64   `json:"exit_basis_z"`
	EntryTrend     float64   `json:"entry_trend"`
	EntryMicro     float64   `json:"entry_micro"`
	EntryScore     float64   `json:"entry_score"`
	FairConfidence float64   `json:"fair_confidence"`
	InitialStop    float64   `json:"initial_stop"`
	MFEUSD         float64   `json:"mfe_usd"`
	MAEUSD         float64   `json:"mae_usd"`
	GrossPnLUSD    float64   `json:"gross_pnl_usd"`
	FundingUSD     float64   `json:"funding_usd"`
	FeesUSD        float64   `json:"fees_usd"`
	NetPnLUSD      float64   `json:"net_pnl_usd"`
	RMultiple      float64   `json:"r_multiple"`
	ExitReason     string    `json:"exit_reason"`
}

type RuntimeStatus struct {
	StartedAt         time.Time               `json:"started_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
	Mode              string                  `json:"mode"`
	Ready             bool                    `json:"ready"`
	PaperVerified     bool                    `json:"paper_verified"`
	OrdersEnabled     bool                    `json:"orders_enabled"`
	LastError         string                  `json:"last_error,omitempty"`
	Books             map[string]BookSnapshot `json:"books,omitempty"`
	FairValue         FairValue               `json:"fair_value"`
	Funding           FundingSnapshot         `json:"funding"`
	Features          Features                `json:"features"`
	Regime            Regime                  `json:"regime"`
	Signal            Signal                  `json:"signal"`
	Account           AccountSnapshot         `json:"account"`
	Risk              RiskDecision            `json:"risk"`
	Position          PositionState           `json:"position"`
	Execution         ExecutionPlan           `json:"execution"`
	LastExchangeEvent time.Time               `json:"last_exchange_event"`
}
