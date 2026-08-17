package diagnostic

import (
	"fmt"
	"math"
	"time"

	"xaut-paper-bot/internal/domain"
)

type Finding struct {
	Severity string         `json:"severity"`
	Code     string         `json:"code"`
	Summary  string         `json:"summary"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

func deriveFindings(report Report) []Finding {
	findings := []Finding{}
	statusProbe := report.Live.Status
	if statusProbe.Error != "" || statusProbe.Status == nil {
		return append(findings, finding("blocking", "status_unavailable", "The diagnostic CLI could not retrieve the bot's live /status response.", map[string]any{"error": statusProbe.Error, "http_status": statusProbe.HTTPStatus}))
	}
	status := *statusProbe.Status
	cfg := report.Config
	now := report.CollectedAt

	staleAfter := 3 * cfg.App.TickInterval.Duration
	if staleAfter < 30*time.Second {
		staleAfter = 30 * time.Second
	}
	if status.UpdatedAt.IsZero() || now.Sub(status.UpdatedAt) > staleAfter {
		findings = append(findings, finding("blocking", "status_stale", "The bot status is stale, suggesting its engine loop is stopped or unable to publish updates.", map[string]any{"updated_at": status.UpdatedAt, "age": now.Sub(status.UpdatedAt).String(), "stale_after": staleAfter.String()}))
	}
	if status.LastError != "" {
		findings = append(findings, finding("warning", "last_engine_error", "The bot reports a recent engine error.", map[string]any{"error": status.LastError}))
	}
	if !status.Ready {
		findings = append(findings, finding("blocking", "not_ready", "The bot is not ready to open a new trade.", nil))
	}
	if cfg.App.ObserveOnly || status.Mode == "public-observe" || status.Mode == "paper-observe" {
		findings = append(findings, finding("blocking", "observe_only", "Order submission is disabled because the bot is in an observe-only mode.", map[string]any{"configured_observe_only": cfg.App.ObserveOnly, "mode": status.Mode}))
	}
	if !status.PaperVerified {
		findings = append(findings, finding("blocking", "paper_account_unverified", "Bitfinex paper-account verification has not succeeded.", map[string]any{"collector_api_key_set": report.CollectorEnvironment.APIKeySet, "collector_api_secret_set": report.CollectorEnvironment.APISecretSet, "note": "collector environment is informational; live paper_verified is authoritative"}))
	}
	if !status.OrdersEnabled {
		findings = append(findings, finding("blocking", "orders_disabled", "The exchange adapter will not submit orders in the current state.", map[string]any{"paper_verified": status.PaperVerified, "configured_observe_only": cfg.App.ObserveOnly, "collector_paper_ack_set": report.CollectorEnvironment.PaperAckSet, "collector_paper_ack_matches": report.CollectorEnvironment.PaperAckMatches, "note": "collector acknowledgment fields are informational unless .env was sourced before running xautdiag"}))
	}

	if !status.Features.Warm {
		findings = append(findings, finding("blocking", "feature_warmup", "Feature initialization is incomplete.", map[string]any{"basis_samples": status.Features.Samples, "required_samples": cfg.Market.WarmupSamples}))
	}
	if !status.FairValue.Valid {
		findings = append(findings, finding("blocking", "fair_value_invalid", "Executable fair value is invalid, so the strategy cannot trade.", map[string]any{"reason": status.FairValue.Reason, "routes": len(status.FairValue.Routes)}))
	}
	if status.FairValue.RouteDispersionBPS > cfg.Market.MaximumRouteDispersionBPS {
		findings = append(findings, finding("blocking", "route_dispersion", "The independent fair-value routes disagree beyond the configured maximum.", map[string]any{"actual_bps": status.FairValue.RouteDispersionBPS, "maximum_bps": cfg.Market.MaximumRouteDispersionBPS}))
	}
	if status.Features.SpreadBPS > cfg.Market.MaximumDirectSpreadBPS {
		findings = append(findings, finding("blocking", "spread_too_wide", "The direct XAUT/USD spread is above the configured trading limit.", map[string]any{"actual_bps": status.Features.SpreadBPS, "maximum_bps": cfg.Market.MaximumDirectSpreadBPS}))
	}
	if !status.Funding.Valid || status.Funding.UpdatedAt.IsZero() || now.Sub(status.Funding.UpdatedAt) > cfg.Funding.MaximumAge.Duration {
		findings = append(findings, finding("warning", "funding_unavailable", "Funding data is unavailable or stale; this specifically blocks new short positions.", map[string]any{"valid": status.Funding.Valid, "updated_at": status.Funding.UpdatedAt, "reason": status.Funding.Reason, "maximum_age": cfg.Funding.MaximumAge.Duration.String()}))
	}

	if status.Regime == domain.RegimeNoTrade || status.Regime == domain.RegimeTransition {
		findings = append(findings, finding("blocking", "regime_blocks_entries", "The current market regime blocks new entries.", map[string]any{"regime": status.Regime, "signal_reason": status.Signal.Reason}))
	}
	if status.Signal.NoNewEntries {
		findings = append(findings, finding("blocking", "signal_blocks_entries", "The strategy explicitly disabled new entries for the current tick.", map[string]any{"reason": status.Signal.Reason, "score": status.Signal.Score, "confidence": status.Signal.Confidence}))
	} else if status.Signal.Score < cfg.Strategy.LongEntryThreshold && status.Signal.Score > -cfg.Strategy.ShortEntryThreshold {
		findings = append(findings, finding("info", "score_inside_dead_band", "The signal score has not crossed either entry threshold.", map[string]any{"score": status.Signal.Score, "long_threshold": cfg.Strategy.LongEntryThreshold, "short_threshold": -cfg.Strategy.ShortEntryThreshold}))
	}
	if status.Signal.Confidence < cfg.Strategy.MinimumConfidence {
		findings = append(findings, finding("blocking", "confidence_too_low", "Signal confidence is below the configured minimum.", map[string]any{"confidence": status.Signal.Confidence, "minimum": cfg.Strategy.MinimumConfidence}))
	}
	if status.Risk.Halt {
		findings = append(findings, finding("blocking", "risk_halt", "Risk management is in a hard-halt state.", map[string]any{"reason": status.Risk.Reason, "flatten": status.Risk.Flatten}))
	} else if !status.Risk.Allowed {
		findings = append(findings, finding("blocking", "risk_rejected", "Risk management did not allow the current target.", map[string]any{"reason": status.Risk.Reason}))
	}
	if status.Execution.Intent == nil {
		findings = append(findings, finding("info", "no_order_intent", "The execution planner has no order to submit on the current tick.", map[string]any{"reason": status.Execution.Reason}))
	}

	for _, symbol := range cfg.Symbols.All() {
		book, ok := status.Books[symbol]
		if !ok {
			findings = append(findings, finding("blocking", "book_missing", "A required WebSocket order book is missing.", map[string]any{"symbol": symbol}))
			continue
		}
		if !book.Valid() {
			findings = append(findings, finding("blocking", "book_invalid", "A required WebSocket order book is invalid.", map[string]any{"symbol": symbol, "bid": book.Bid, "ask": book.Ask, "bid_qty": book.BidQty, "ask_qty": book.AskQty}))
		} else if now.Sub(book.UpdatedAt) > cfg.Market.MaximumBookAge.Duration {
			findings = append(findings, finding("blocking", "book_stale", "A required WebSocket order book is stale.", map[string]any{"symbol": symbol, "updated_at": book.UpdatedAt, "age": now.Sub(book.UpdatedAt).String(), "maximum_age": cfg.Market.MaximumBookAge.Duration.String()}))
		}
	}

	if halt, ok := report.StateFiles["halt_file"]; ok && halt.Exists {
		findings = append(findings, finding("blocking", "halt_file_present", "The configured HALT file exists and prevents new trading.", map[string]any{"path": halt.Path, "modified_at": halt.ModifiedAt}))
	}
	if report.Log.RateLimitLinesSeen > 0 {
		findings = append(findings, finding("warning", "recent_rate_limits", "The inspected log tail contains REST rate-limit errors.", map[string]any{"lines_seen": report.Log.RateLimitLinesSeen, "bytes_inspected": report.Log.BytesInspected}))
	}
	if report.Recent.Trades.TotalRecords == 0 {
		findings = append(findings, finding("info", "no_completed_trades", "No completed trade records were found in the performance ledger.", nil))
	}
	if report.Recent.Fills.TotalRecords == 0 {
		findings = append(findings, finding("info", "no_fills", "No authenticated paper fills were found.", nil))
	}
	if math.Abs(status.Position.QuantityXAUT) > 1e-9 {
		findings = append(findings, finding("info", "position_open", "The bot currently observes an open XAUT position.", map[string]any{"quantity_xaut": status.Position.QuantityXAUT, "average_entry": status.Position.AverageEntry, "stop_price": status.Position.StopPrice}))
	}
	return findings
}

func finding(severity, code, summary string, evidence map[string]any) Finding {
	return Finding{Severity: severity, Code: code, Summary: summary, Evidence: evidence}
}

func FindingsSummary(findings []Finding) string {
	blocking, warnings := 0, 0
	for _, item := range findings {
		switch item.Severity {
		case "blocking":
			blocking++
		case "warning":
			warnings++
		}
	}
	return fmt.Sprintf("%d blocking, %d warning, %d total findings", blocking, warnings, len(findings))
}
