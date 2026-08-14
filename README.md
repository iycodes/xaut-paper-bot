# XAUT Paper Trading Bot — v2 Engine Rewrite

A modular Go paper-trading research bot for Bitfinex XAUT. Long exposure is spot-only; short exposure is margin-only. The application is compile-time paper-only and defaults to observe-only.

> This software is for paper-trading research. It does not guarantee profitability and contains no real-account order mode.

## Trading engine

The decision chain is deliberately hierarchical:

`market health -> 15m/1h/4h regime -> executable fair-value opportunity -> trade-flow confirmation -> expected edge after uncertainty/funding -> stop-risk sizing -> execution`

### Signals and regimes

- **Trend:** independently closed 15-minute, 1-hour and 4-hour bars; no 5-second pseudo-timeframes.
- **Fair value:** side-specific executable synthetic bid/ask from `XAUT/UST × UST/USD` and `XAUT/BTC × BTC/USD`. Route midpoints are not used to approve an executable edge.
- **Basis:** minute-sampled rolling log basis and z-score to avoid variance compression from repeatedly sampling an unchanged book.
- **Microstructure:** multi-level book imbalance, executed trade flow, microprice and flow persistence.
- **Regimes:** `trend`, `range`, `dislocation`, `transition`, `no_trade`. Strong trend has priority over dislocation; mean reversion is vetoed while trend/volatility/basis state is unstable.

### Short economics

A margin short is blocked when funding data is stale/unavailable. The strategy estimates expected funding from recent Bitfinex XAUT funding trades and requires funding to remain below the configured share of gross expected edge. It then subtracts funding, fair-value uncertainty and a short-specific safety buffer before allowing the trade.

### Risk management defaults for the $30,000 research account

- position-sizing equity capped at `$30,000`
- drawdown monitoring uses **actual uncapped account equity**
- risk per position: `0.20%` (`$60` at $30k)
- maximum aggregate open stop risk: `0.50%`
- absolute gross exposure ceiling: `1.00x`
- soft daily throttles: `$100 -> 75% risk`, `$150 -> 50% risk`
- daily hard loss: `$225`
- weekly hard loss: `$600`
- maximum high-water drawdown: `$1,500`
- three consecutive realized losing trades: hard halt
- no martingale and no margin longs

Target sizing is validated against the **simulated dollar loss at the active stop**, not just a volatility-derived notional estimate. Pending opening orders are included in the worst-case 1x calculation.

### Stops and exits

- An exchange-side `STOP` order is maintained separately from normal working orders.
- The software stop remains as an independent backup watchdog.
- Stop changes caused by trailing logic trigger protective-order refresh.
- Mean-reversion/dislocation positions exit when the basis normalizes.
- Trend positions exit when the trend thesis deteriorates, or through stop/time protection.
- A stopped direction is blocked from immediate re-entry until both a cooldown and thesis-reset condition are satisfied.

### Execution

- Flatten before reversal: spot long and margin short are never intentionally held together.
- Normal entries use post-only limits.
- Risk exits use bounded marketable limits.
- Child orders are capped by the minimum of remaining target, configured `$5k` ceiling, configured share of current book depth, and configured share of recent traded notional.
- Normal orders and protective stops have separate Bitfinex GIDs to prevent stale stop/order conflicts.

### Live paper analytics

Authenticated paper fills are stored in `data/fills.jsonl`; completed trades are stored in `data/trades.jsonl`. The persistent ledger records fill-based entry/exit VWAP, realized P&L, fees, funding estimate, R multiple, MFE/MAE, entry regime, basis, trend, micro score, combined score and fair-value confidence. Consecutive-loss risk state is driven by completed fill-based trades rather than a market-price approximation.

## Bitfinex markets

The default configuration uses live public XAUT markets to generate signals and Bitfinex's paper symbol for execution:

```text
signal/reference: tXAUT:USD
                  tXAUT:UST × tUSTUSD
                  tXAUT:BTC × tBTCUSD
funding reference: fXAUT
paper execution:  tTESTXAUT:TESTUSD
```

## Safe start

```bash
cp .env.example .env
go mod tidy
go test ./...
go run ./cmd/xautbot -config configs/config.json
```

The default has `"observe_only": true`, so no orders are sent.

To enable **Bitfinex paper-account orders only**, add paper-account API credentials to `.env`, verify they belong to a Bitfinex paper account, set `observe_only` to `false`, and set the exact acknowledgement environment variable shown in `.env.example`. The adapter checks Bitfinex's paper-trading account flag before enabling order submission. It has no real-account trading switch.

## HTTP monitoring

```bash
curl http://127.0.0.1:8082/status
curl http://127.0.0.1:8082/healthz
curl http://127.0.0.1:8082/readyz
```

Create the configured `HALT` file to block new trading and invoke configured hard-halt behavior.

## Supervisor on Linux

Build the binary before starting Supervisor:

```bash
mkdir -p bin
go mod tidy
go test ./...
go build -trimpath -o bin/xautbot ./cmd/xautbot
chmod +x scripts/run-xautbot.sh
```

The standalone `supervisord.conf` uses paths relative to its own location and
loads `.env` through `scripts/run-xautbot.sh` when that file exists.

```bash
supervisord -c "$PWD/supervisord.conf"
supervisorctl -c "$PWD/supervisord.conf" status
supervisorctl -c "$PWD/supervisord.conf" restart xaut-paper-bot
supervisorctl -c "$PWD/supervisord.conf" stop xaut-paper-bot
```

Runtime output is written to `xautbot.log`. Supervisor's own log, PID and
control socket are also kept in the project directory.

## Package layout

```text
cmd/xautbot/                  process entry point
internal/app/                 engine orchestration
internal/exchange/bitfinex/   official Bitfinex Go SDK adapter
internal/fairvalue/           executable synthetic fair value
internal/features/            MTF trend/basis/microstructure
internal/regime/              trend/range/dislocation/transition gating
internal/strategy/            signal, short economics, re-entry state
internal/risk/                stop-risk sizing, exposure/drawdown limits
internal/position/            software watchdog + trailing state
internal/execution/           child orders + exchange protective stops
internal/performance/         fill-based paper performance ledger
internal/monitor/             health/status HTTP server
internal/journal/             event journal
configs/                      default/example configuration
```

## Validation

The engine-only packages can be tested without network access:

```bash
go test ./internal/config ./internal/fairvalue ./internal/features ./internal/regime ./internal/strategy ./internal/risk ./internal/execution ./internal/performance ./internal/position ./internal/app
go vet  ./internal/config ./internal/fairvalue ./internal/features ./internal/regime ./internal/strategy ./internal/risk ./internal/execution ./internal/performance ./internal/position
```

A complete `go test ./...` additionally downloads the pinned official Bitfinex SDK from `go.mod` and generates/updates `go.sum` on a networked Go environment.
