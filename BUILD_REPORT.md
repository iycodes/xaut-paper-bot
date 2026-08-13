# Engine Rewrite Build Report

## Mandatory trading-engine fixes implemented

- Proper 15m / 1h / 4h trend frames seeded from Bitfinex candles.
- Executable synthetic bid/ask fair value instead of midpoint-only fair value.
- Trend priority and explicit mean-reversion trend veto.
- Transition/high-volatility no-entry regime.
- Actual public trade flow plus multi-level depth in the microstructure score.
- Live XAUT funding-trade economics gate for margin shorts.
- Distinct mean-reversion and trend exit logic.
- Exchange-side protective STOP orders plus independent software backup stops.
- Stop-out re-entry cooldown and thesis-reset requirement.
- Actual stop-loss dollar-risk simulation before target approval.
- Actual uncapped account equity for high-water drawdown; capped equity only for sizing.
- Soft daily risk throttles plus existing hard daily/weekly/drawdown limits.
- Dynamic child-order caps from order-book depth and recent traded volume.
- Fill-based persistent paper performance ledger and fill-based closed P&L.
- Protective-stop and normal-order GIDs separated.

## Local validation completed

The rewritten engine packages passed unit tests and `go vet`. Tests explicitly cover executable fair-value sides, seeded multi-timeframe trend state, funding-gated shorts, stop re-entry blocking, actual stop-risk / 1x sizing, uncapped high-water drawdown, trend priority over dislocation, transition blocking, and software backup stop behavior.

The container used for this rewrite cannot resolve external Go modules, so a complete build against the remotely hosted SDK could not be executed here. The adapter was checked against the current official SDK API documentation/source shape; on a networked machine run `go mod tidy && go test ./... && go vet ./...` before starting paper trading.

## Paper-only invariant

The Bitfinex adapter retains `PaperOnlyBuild = true`, requires the authenticated Bitfinex paper-trading flag, defaults to observe-only, and has no setting that enables real-account orders.
