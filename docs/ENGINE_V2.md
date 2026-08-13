# Engine v2 decision model

The engine deliberately does not turn a weighted indicator score directly into an order.

1. Verify book freshness, route agreement, spread, account freshness and risk state.
2. Update independent 15m/1h/4h bars and classify trend/transition/range/dislocation.
3. Estimate executable fair bid/ask from independent XAUT routes.
4. Compute basis, multi-level depth and actual aggressive trade-flow features.
5. Apply regime-specific weights and direction-specific entry thresholds.
6. For shorts, require fresh XAUT funding information and positive expected edge after funding/uncertainty/short buffer.
7. Translate signal to a target, then size from dollar loss at stop and hard portfolio caps.
8. Flatten opposing inventory before opening the other side.
9. Maintain a separate exchange protective stop and software backup stop.
10. Attribute completed paper trades from actual fills and feed realized results into risk halts.

No component claims or guarantees profitability. Paper observations are intended to determine whether the edge survives real-time spread, fill, funding and regime conditions before any future design decisions.
