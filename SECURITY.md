# Security policy

## Paper-trading boundary

This repository is intentionally limited to Bitfinex paper trading. It has no configuration switch for real-account order submission.

The Bitfinex adapter applies all of these checks before submitting an order:

1. The compile-time `PaperOnlyBuild` invariant must be enabled.
2. The authenticated `/v2/auth/r/info/user` response must report `PPT_ENABLED == 1`.
3. `app.observe_only` must be `false`.
4. `BFX_PAPER_TRADING_ACK` must exactly equal `I_UNDERSTAND_PAPER_ONLY`.
5. The account snapshot must be recent and the proposed order must pass independent venue, inventory, and 1.0× gross-exposure checks.

Do not weaken or remove these controls. Do not use a real-account API key.

## API-key permissions

Create a dedicated API key on a Bitfinex paper-trading subaccount. Grant only the account-read and order-management permissions required by the bot. Never grant withdrawal permission, and never place secrets in the repository or configuration JSON.

## Secret handling

Store credentials in environment variables. `.env` is excluded from Git, but it is still a plaintext local file; restrict its operating-system permissions. Rotate the key immediately if it is exposed.

## Network exposure

The status server does not provide authentication. The supplied Compose configuration binds it to `127.0.0.1`. Keep it private or place it behind authenticated infrastructure.

## Incident response

Create `data/HALT` to block new entries and request a best-effort paper-position flatten when `flatten_on_hard_halt` is enabled. Then inspect:

- `data/risk_state.json`
- `data/position_state.json`
- `data/events.jsonl`
- the Bitfinex paper account directly

A persisted hard halt is deliberately latched. Do not delete or edit state files until the cause has been reviewed and all paper positions and orders have been reconciled.

## Reporting a problem

When reporting a defect, remove API credentials, wallet identifiers, and any other sensitive account data from logs and screenshots.
