package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.Duration.String()) }

type Config struct {
	App       AppConfig       `json:"app"`
	Bitfinex  BitfinexConfig  `json:"bitfinex"`
	Symbols   SymbolConfig    `json:"symbols"`
	Market    MarketConfig    `json:"market"`
	Strategy  StrategyConfig  `json:"strategy"`
	Risk      RiskConfig      `json:"risk"`
	Execution ExecutionConfig `json:"execution"`
	Funding   FundingConfig   `json:"funding"`
	Gold      GoldConfig      `json:"gold"`
}

type AppConfig struct {
	Name              string   `json:"name"`
	TickInterval      Duration `json:"tick_interval"`
	AccountRefresh    Duration `json:"account_refresh"`
	HTTPAddress       string   `json:"http_address"`
	DataDirectory     string   `json:"data_directory"`
	ObserveOnly       bool     `json:"observe_only"`
	FlattenOnHardHalt bool     `json:"flatten_on_hard_halt"`
	CancelOnShutdown  bool     `json:"cancel_on_shutdown"`
}
type BitfinexConfig struct {
	APIKeyEnv        string   `json:"api_key_env"`
	APISecretEnv     string   `json:"api_secret_env"`
	PaperAckEnv      string   `json:"paper_ack_env"`
	PaperAckValue    string   `json:"paper_ack_value"`
	ReconnectEvery   Duration `json:"reconnect_every"`
	ReconnectTries   int      `json:"reconnect_tries"`
	HeartbeatTimeout Duration `json:"heartbeat_timeout"`
	USTHaircut       float64  `json:"ust_haircut"`
	PublicAPIBase    string   `json:"public_api_base"`
}
type SymbolConfig struct {
	OrderPair   string `json:"order_pair"`
	XAUTUSD     string `json:"xaut_usd"`
	XAUTUST     string `json:"xaut_ust"`
	USTUSD      string `json:"ust_usd"`
	XAUTBTC     string `json:"xaut_btc"`
	BTCUSD      string `json:"btc_usd"`
	XAUTFunding string `json:"xaut_funding"`
}

func (s SymbolConfig) All() []string {
	seen := map[string]bool{}
	out := make([]string, 0, 6)
	for _, v := range []string{s.OrderPair, s.XAUTUSD, s.XAUTUST, s.USTUSD, s.XAUTBTC, s.BTCUSD} {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

type MarketConfig struct {
	BookDepthBPS                float64  `json:"book_depth_bps"`
	MaximumBookAge              Duration `json:"maximum_book_age"`
	MaximumTradeAge             Duration `json:"maximum_trade_age"`
	MaximumDirectSpreadBPS      float64  `json:"maximum_direct_spread_bps"`
	MaximumRouteDispersionBPS   float64  `json:"maximum_route_dispersion_bps"`
	FairValueModelBufferBPS     float64  `json:"fair_value_model_buffer_bps"`
	WarmupSamples               int      `json:"warmup_samples"`
	BasisWindow                 int      `json:"basis_window"`
	MicroDepthLevels            int      `json:"micro_depth_levels"`
	TradeFlowLookback           Duration `json:"trade_flow_lookback"`
	PublicTradesRefresh         Duration `json:"public_trades_refresh"`
	TransitionVolRatio          float64  `json:"transition_vol_ratio"`
	TransitionTrendAcceleration float64  `json:"transition_trend_acceleration"`
	TransitionBasisInstability  float64  `json:"transition_basis_instability"`
	HighVolatilityFraction      float64  `json:"high_volatility_fraction"`
	Trend15mWeight              float64  `json:"trend_15m_weight"`
	Trend1hWeight               float64  `json:"trend_1h_weight"`
	Trend4hWeight               float64  `json:"trend_4h_weight"`
}

type Weights struct {
	Trend float64 `json:"trend"`
	Basis float64 `json:"basis"`
	Micro float64 `json:"micro"`
}
type StrategyConfig struct {
	RangeWeights            Weights  `json:"range_weights"`
	TrendWeights            Weights  `json:"trend_weights"`
	DislocationWeights      Weights  `json:"dislocation_weights"`
	TrendRegimeThreshold    float64  `json:"trend_regime_threshold"`
	DislocationZThreshold   float64  `json:"dislocation_z_threshold"`
	MeanReversionTrendVeto  float64  `json:"mean_reversion_trend_veto"`
	LongEntryThreshold      float64  `json:"long_entry_threshold"`
	ShortEntryThreshold     float64  `json:"short_entry_threshold"`
	TrendExitThreshold      float64  `json:"trend_exit_threshold"`
	MeanReversionExitZ      float64  `json:"mean_reversion_exit_z"`
	LongNormalCap           float64  `json:"long_normal_cap"`
	ShortNormalCap          float64  `json:"short_normal_cap"`
	HighConfidenceShortCap  float64  `json:"high_confidence_short_cap"`
	HighConfidenceThreshold float64  `json:"high_confidence_threshold"`
	MinimumConfidence       float64  `json:"minimum_confidence"`
	MinimumExpectedEdgeBPS  float64  `json:"minimum_expected_edge_bps"`
	ShortExtraBufferBPS     float64  `json:"short_extra_buffer_bps"`
	ThesisResetBasisZ       float64  `json:"thesis_reset_basis_z"`
	ThesisResetTrend        float64  `json:"thesis_reset_trend"`
	ReentryCooldown         Duration `json:"reentry_cooldown"`
}

type RiskConfig struct {
	CapitalBaseUSD           float64  `json:"capital_base_usd"`
	RiskPerTradeFraction     float64  `json:"risk_per_trade_fraction"`
	MaximumOpenRiskFraction  float64  `json:"maximum_open_risk_fraction"`
	AbsoluteGrossExposure    float64  `json:"absolute_gross_exposure"`
	MinimumStopFraction      float64  `json:"minimum_stop_fraction"`
	VolatilityStopMultiplier float64  `json:"volatility_stop_multiplier"`
	DailySoftLoss1USD        float64  `json:"daily_soft_loss_1_usd"`
	DailySoftLoss1Throttle   float64  `json:"daily_soft_loss_1_throttle"`
	DailySoftLoss2USD        float64  `json:"daily_soft_loss_2_usd"`
	DailySoftLoss2Throttle   float64  `json:"daily_soft_loss_2_throttle"`
	DailyHardLossUSD         float64  `json:"daily_hard_loss_usd"`
	WeeklyHardLossUSD        float64  `json:"weekly_hard_loss_usd"`
	MaximumDrawdownUSD       float64  `json:"maximum_drawdown_usd"`
	MaximumConsecutiveLosses int      `json:"maximum_consecutive_losses"`
	LiquidityParticipation   float64  `json:"liquidity_participation"`
	MinimumOrderNotionalUSD  float64  `json:"minimum_order_notional_usd"`
	AccountMaximumAge        Duration `json:"account_maximum_age"`
	TrailingActivationR      float64  `json:"trailing_activation_r"`
	TrailingDistanceR        float64  `json:"trailing_distance_r"`
	MaximumHoldingTime       Duration `json:"maximum_holding_time"`
	HaltFile                 string   `json:"halt_file"`
}

type ExecutionConfig struct {
	GroupID                   int64    `json:"group_id"`
	StopGroupID               int64    `json:"stop_group_id"`
	MinimumXAUTQuantity       float64  `json:"minimum_xaut_quantity"`
	QuantityStep              float64  `json:"quantity_step"`
	PriceStep                 float64  `json:"price_step"`
	TargetToleranceXAUT       float64  `json:"target_tolerance_xaut"`
	MaximumChildNotionalUSD   float64  `json:"maximum_child_notional_usd"`
	DepthParticipation        float64  `json:"depth_participation"`
	RecentVolumeParticipation float64  `json:"recent_volume_participation"`
	MaximumOrderAge           Duration `json:"maximum_order_age"`
	MinimumSubmitInterval     Duration `json:"minimum_submit_interval"`
	UrgentSlippageBPS         float64  `json:"urgent_slippage_bps"`
	ProtectiveStops           bool     `json:"protective_stops"`
}

type FundingConfig struct {
	FallbackDailyRate    float64  `json:"fallback_daily_rate"`
	ExpectedHoldingHours float64  `json:"expected_holding_hours"`
	MaximumEdgeShare     float64  `json:"maximum_edge_share"`
	MaximumAge           Duration `json:"maximum_age"`
	Lookback             Duration `json:"lookback"`
	RefreshInterval      Duration `json:"refresh_interval"`
}
type GoldConfig struct {
	Enabled             bool     `json:"enabled"`
	URL                 string   `json:"url"`
	PriceJSONField      string   `json:"price_json_field"`
	MaximumAge          Duration `json:"maximum_age"`
	BasisEWMAAlpha      float64  `json:"basis_ewma_alpha"`
	MaximumDeviationBPS float64  `json:"maximum_deviation_bps"`
}

func Default() Config {
	return Config{
		App:       AppConfig{Name: "xaut-paper-bot", TickInterval: Duration{5 * time.Second}, AccountRefresh: Duration{10 * time.Second}, HTTPAddress: ":8082", DataDirectory: "data", ObserveOnly: true, FlattenOnHardHalt: true, CancelOnShutdown: true},
		Bitfinex:  BitfinexConfig{APIKeyEnv: "BITFINEX_API_KEY", APISecretEnv: "BITFINEX_API_SECRET", PaperAckEnv: "BFX_PAPER_TRADING_ACK", PaperAckValue: "I_UNDERSTAND_PAPER_ONLY", ReconnectEvery: Duration{3 * time.Second}, ReconnectTries: 100, HeartbeatTimeout: Duration{20 * time.Second}, USTHaircut: .995, PublicAPIBase: "https://api-pub.bitfinex.com/v2"},
		Symbols:   SymbolConfig{OrderPair: "tTESTXAUT:TESTUSD", XAUTUSD: "tXAUT:USD", XAUTUST: "tXAUT:UST", USTUSD: "tUSTUSD", XAUTBTC: "tXAUT:BTC", BTCUSD: "tBTCUSD", XAUTFunding: "fXAUT"},
		Market:    MarketConfig{BookDepthBPS: 10, MaximumBookAge: Duration{15 * time.Second}, MaximumTradeAge: Duration{30 * time.Second}, MaximumDirectSpreadBPS: 20, MaximumRouteDispersionBPS: 35, FairValueModelBufferBPS: 3, WarmupSamples: 80, BasisWindow: 240, MicroDepthLevels: 5, TradeFlowLookback: Duration{60 * time.Second}, PublicTradesRefresh: Duration{10 * time.Second}, TransitionVolRatio: 1.6, TransitionTrendAcceleration: .30, TransitionBasisInstability: .75, HighVolatilityFraction: .012, Trend15mWeight: .20, Trend1hWeight: .35, Trend4hWeight: .45},
		Strategy:  StrategyConfig{RangeWeights: Weights{Trend: .15, Basis: .70, Micro: .15}, TrendWeights: Weights{Trend: .65, Basis: .20, Micro: .15}, DislocationWeights: Weights{Trend: .10, Basis: .80, Micro: .10}, TrendRegimeThreshold: .45, DislocationZThreshold: 2.1, MeanReversionTrendVeto: .32, LongEntryThreshold: .38, ShortEntryThreshold: .48, TrendExitThreshold: .15, MeanReversionExitZ: .55, LongNormalCap: .80, ShortNormalCap: .60, HighConfidenceShortCap: .80, HighConfidenceThreshold: .90, MinimumConfidence: .40, MinimumExpectedEdgeBPS: 8, ShortExtraBufferBPS: 4, ThesisResetBasisZ: 1.0, ThesisResetTrend: .20, ReentryCooldown: Duration{20 * time.Minute}},
		Risk:      RiskConfig{CapitalBaseUSD: 30_000, RiskPerTradeFraction: .002, MaximumOpenRiskFraction: .005, AbsoluteGrossExposure: 1, MinimumStopFraction: .0035, VolatilityStopMultiplier: 1.75, DailySoftLoss1USD: 100, DailySoftLoss1Throttle: .75, DailySoftLoss2USD: 150, DailySoftLoss2Throttle: .50, DailyHardLossUSD: 225, WeeklyHardLossUSD: 600, MaximumDrawdownUSD: 1500, MaximumConsecutiveLosses: 3, LiquidityParticipation: .05, MinimumOrderNotionalUSD: 25, AccountMaximumAge: Duration{30 * time.Second}, TrailingActivationR: 1.5, TrailingDistanceR: 1.0, MaximumHoldingTime: Duration{72 * time.Hour}, HaltFile: "HALT"},
		Execution: ExecutionConfig{GroupID: 883301, StopGroupID: 883302, MinimumXAUTQuantity: .002, QuantityStep: .0001, PriceStep: .1, TargetToleranceXAUT: .001, MaximumChildNotionalUSD: 5000, DepthParticipation: .05, RecentVolumeParticipation: .10, MaximumOrderAge: Duration{45 * time.Second}, MinimumSubmitInterval: Duration{5 * time.Second}, UrgentSlippageBPS: 8, ProtectiveStops: true},
		Funding:   FundingConfig{FallbackDailyRate: .0003, ExpectedHoldingHours: 8, MaximumEdgeShare: .20, MaximumAge: Duration{10 * time.Minute}, Lookback: Duration{6 * time.Hour}, RefreshInterval: Duration{time.Minute}},
		Gold:      GoldConfig{Enabled: false, URL: "", PriceJSONField: "price", MaximumAge: Duration{2 * time.Minute}, BasisEWMAAlpha: .02, MaximumDeviationBPS: 100},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, cfg.Validate()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	if !filepath.IsAbs(cfg.App.DataDirectory) {
		cfg.App.DataDirectory = filepath.Clean(cfg.App.DataDirectory)
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var errs []string
	if c.App.TickInterval.Duration <= 0 {
		errs = append(errs, "app.tick_interval must be positive")
	}
	if c.App.AccountRefresh.Duration <= 0 {
		errs = append(errs, "app.account_refresh must be positive")
	}
	if c.App.DataDirectory == "" {
		errs = append(errs, "app.data_directory is required")
	}
	for name, symbol := range map[string]string{"order_pair": c.Symbols.OrderPair, "xaut_usd": c.Symbols.XAUTUSD, "xaut_ust": c.Symbols.XAUTUST, "ust_usd": c.Symbols.USTUSD, "xaut_btc": c.Symbols.XAUTBTC, "btc_usd": c.Symbols.BTCUSD} {
		if !strings.HasPrefix(symbol, "t") {
			errs = append(errs, "symbols."+name+" must start with t")
		}
	}
	if c.Symbols.XAUTFunding != "" && !strings.HasPrefix(c.Symbols.XAUTFunding, "f") {
		errs = append(errs, "symbols.xaut_funding must start with f")
	}
	if c.Risk.CapitalBaseUSD <= 0 {
		errs = append(errs, "risk.capital_base_usd must be positive")
	}
	if c.Risk.AbsoluteGrossExposure <= 0 || c.Risk.AbsoluteGrossExposure > 1 {
		errs = append(errs, "risk.absolute_gross_exposure must be in (0,1]")
	}
	if c.Strategy.LongNormalCap > c.Risk.AbsoluteGrossExposure || c.Strategy.ShortNormalCap > c.Risk.AbsoluteGrossExposure || c.Strategy.HighConfidenceShortCap > c.Risk.AbsoluteGrossExposure {
		errs = append(errs, "strategy exposure caps cannot exceed absolute gross exposure")
	}
	if c.Risk.RiskPerTradeFraction <= 0 || c.Risk.RiskPerTradeFraction > .01 {
		errs = append(errs, "risk.risk_per_trade_fraction must be in (0,0.01]")
	}
	if c.Risk.MaximumOpenRiskFraction < c.Risk.RiskPerTradeFraction || c.Risk.MaximumOpenRiskFraction > .02 {
		errs = append(errs, "risk.maximum_open_risk_fraction invalid")
	}
	if c.Risk.MinimumStopFraction <= 0 || c.Risk.MinimumStopFraction > .25 {
		errs = append(errs, "risk.minimum_stop_fraction invalid")
	}
	if c.Execution.MinimumXAUTQuantity <= 0 || c.Execution.QuantityStep <= 0 || c.Execution.PriceStep <= 0 {
		errs = append(errs, "execution quantity and price increments must be positive")
	}
	if c.Execution.DepthParticipation <= 0 || c.Execution.DepthParticipation > 1 || c.Execution.RecentVolumeParticipation <= 0 || c.Execution.RecentVolumeParticipation > 1 {
		errs = append(errs, "execution participation fractions must be in (0,1]")
	}
	if c.Funding.MaximumEdgeShare < 0 || c.Funding.MaximumEdgeShare > 1 {
		errs = append(errs, "funding.maximum_edge_share must be in [0,1]")
	}
	if c.Market.PublicTradesRefresh.Duration <= 0 {
		errs = append(errs, "market.public_trades_refresh must be positive")
	}
	if c.Funding.RefreshInterval.Duration <= 0 {
		errs = append(errs, "funding.refresh_interval must be positive")
	}
	if c.Strategy.MeanReversionTrendVeto <= 0 || c.Strategy.MeanReversionTrendVeto >= 1 {
		errs = append(errs, "strategy.mean_reversion_trend_veto must be in (0,1)")
	}
	if c.Market.Trend15mWeight+c.Market.Trend1hWeight+c.Market.Trend4hWeight < .999 || c.Market.Trend15mWeight+c.Market.Trend1hWeight+c.Market.Trend4hWeight > 1.001 {
		errs = append(errs, "market trend timeframe weights must sum to 1")
	}
	if c.Market.WarmupSamples < 2 || c.Market.BasisWindow < c.Market.WarmupSamples || c.Market.BasisWindow > 1000 {
		errs = append(errs, "market basis_window must be between warmup_samples and 1000, and warmup_samples must be at least 2")
	}
	for name, w := range map[string]Weights{"range": c.Strategy.RangeWeights, "trend": c.Strategy.TrendWeights, "dislocation": c.Strategy.DislocationWeights} {
		if abs(w.Trend+w.Basis+w.Micro-1) > 1e-9 {
			errs = append(errs, fmt.Sprintf("strategy.%s_weights must sum to 1", name))
		}
	}
	if c.Gold.Enabled && (c.Gold.URL == "" || c.Gold.PriceJSONField == "") {
		errs = append(errs, "gold.url and gold.price_json_field are required when gold.enabled=true")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
