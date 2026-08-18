# Architecture

## Design boundary

The application is a live-market, paper-order engine. It contains no historical replay or backtesting subsystem. Public order books drive the model; authenticated REST snapshots reconcile the paper account.

```text
Bitfinex public WebSocket books
            |
            v
  market snapshot normalization
            |
            +--> live signal book: XAUT/USD
            |       +--> fair value (XAUT/UST x UST/USD, XAUT/BTC x BTC/USD)
            |       +--> features (basis z-score, trend/volatility, microprice)
            |
            +--> paper execution book: TESTXAUT/TESTUSD
                            |
                            v
                    regime classifier
                            |
                            v
                      signed signal
                            |
                            v
                 independent risk manager
                            |
                            v
                    target XAUT quantity
                            |
                            v
                    execution planner
                            |
                    paper-only guard
                            |
                            v
              Bitfinex paper REST order API
```

## Module map

| Package | Responsibility |
|---|---|
| `internal/exchange/bitfinex` | Official SDK adapter, book subscriptions, account reconciliation, paper-account verification, final order guards |
| `internal/fairvalue` | Independent cross-route XAUT/USD estimate and uncertainty |
| `internal/features` | Rolling basis, volatility-normalized trend, spread and microstructure features |
| `internal/regime` | Range, trend, dislocation and no-trade classification |
| `internal/strategy` | Regime-weighted signed signal and asymmetric short penalty |
| `internal/risk` | $30,000 risk base, stop sizing, liquidity cap, open-order exposure, loss and drawdown halts |
| `internal/position` | Persistent software stop, trailing stop, maximum holding time, approximate close-event P&L |
| `internal/execution` | Spot-long/margin-short target reconciliation and child-order planning |
| `internal/journal` | Append-only JSONL operational events |
| `internal/monitor` | Health, readiness and runtime status endpoints |
| `internal/app` | Single authoritative orchestration loop |

## Long and short mapping

A positive target is implemented only as a paper exchange-wallet spot position:

```text
positive target -> EXCHANGE LIMIT buy/sell -> spot TESTXAUT
```

A negative target is implemented only as a margin position:

```text
negative target -> LIMIT sell on TESTXAUT:TESTUSD -> margin short
close short      -> LIMIT buy with Close=true
```

The planner never intentionally opens a margin long. It also refuses to open a short while spot XAUT remains, or a spot long while a margin short remains. A reversal is therefore a two-stage transition: flatten, reconcile, then open the opposite venue.

## Signal and execution books

The model uses the live `XAUT:USD` book for basis, trend and microstructure. Actual paper orders use the distinct `TESTXAUT:TESTUSD` book for limit prices, spread, depth and software-stop triggers. Keeping these roles separate prevents a model reference symbol from being submitted as the paper order symbol.

## Fair value

Two independent USD routes are formed from executable market mids:

```text
route 1 = XAUT/UST x UST/USD
route 2 = XAUT/BTC x BTC/USD
```

Both routes must be fresh and valid. The engine rejects excessive disagreement, calculates a weighted estimate, and adds route dispersion, route spread and a configurable model buffer to uncertainty.

## Signal pipeline

Each feature is normalized to approximately `[-1, +1]`:

- Positive trend score: upward trend relative to realized volatility.
- Positive basis score: direct XAUT/USD is below cross-route fair value.
- Positive micro score: book microprice indicates buy-side pressure.

Weights depend on regime. The combined score is converted into a signed target exposure. Negative signals face a stricter entry threshold and explicit funding/short-risk penalties.

## Risk ordering

The target can only shrink as it passes through risk controls:

```text
strategy exposure cap
  -> per-trade stop-risk cap
  -> aggregate open-risk cap
  -> order-book participation cap
  -> final target exposure
  -> projected exposure including pending opening orders
  -> 1.0x absolute gross cap
```

Reductions and emergency flattening are allowed to bypass the entry-liquidity cap so the bot is not trapped by its own entry constraint.

## Position protection

The persistent position tracker arms a stop fraction before an opening order is sent. It activates after the paper account confirms a position, then:

- never widens the initial stop;
- tightens after favorable movement reaches the configured R threshold;
- triggers on the executable side of the paper execution book;
- exits after the maximum holding time;
- persists state across restarts.

These are software-managed exits. They require the process, network, exchange API and fresh book data to remain available.

## State and recovery

Persistent files in `data/`:

```text
risk_state.json       loss counters, equity anchors, latched hard halt
position_state.json   observed position and software stop
basis_state.json      rolling timestamped basis samples and their source
events.jsonl           planned orders, submissions, cancellations and errors
```

After restart, the application restores recent exact live-book basis samples.
If the state is absent, insufficient or stale, it requests aligned closed
1-minute candles for XAUT/USD and both synthetic routes. Candle closes initialize
the statistical basis only; fresh executable WebSocket books are still required
for readiness and every order decision. The application also fetches the paper
account before it can submit anything. The final exchange adapter rejects orders
when its cached account snapshot is stale.

## Concurrency model

The SDK manages network activity in the background. The application performs all strategy, risk and execution decisions from one timed orchestration loop. Shared exchange caches and monitoring status use narrow mutex or atomic protection; order submission is serialized to avoid nonce and account-state races.

Public trade history and the current funding ticker use separate cached refresh schedules rather
than running on every engine tick. A rate-limit response applies a shared pause
to both trade-history streams, followed by bounded exponential backoff. This
keeps WebSocket-driven book processing responsive without exhausting REST
capacity.

The transition ratios compare recent variability with their longer baseline;
their neutral level is approximately `1.0`. The configured transition gates
therefore remain above `1.0` (the default basis-instability gate is `1.6`) so a
stable baseline does not accidentally become a permanent no-entry regime.
