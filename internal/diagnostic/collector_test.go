package diagnostic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

func TestCollectExplainsNoTradeAndOmitsSecrets(t *testing.T) {
	cfg := config.Default()
	now := time.Now().UTC()
	books := make(map[string]domain.BookSnapshot)
	for _, symbol := range cfg.Symbols.All() {
		books[symbol] = domain.BookSnapshot{Symbol: symbol, Bid: 100, Ask: 101, BidQty: 1, AskQty: 1, UpdatedAt: now}
	}
	status := domain.RuntimeStatus{
		StartedAt: now.Add(-time.Hour), UpdatedAt: now, Mode: "public-observe", Ready: false,
		Books: books, FairValue: domain.FairValue{Valid: true, Bid: 100, Ask: 101, Price: 100.5, UpdatedAt: now},
		Features: domain.Features{Samples: 10, Warm: false, SpreadBPS: 2}, Regime: domain.RegimeNoTrade,
		Signal: domain.Signal{NoNewEntries: true, Reason: "multi-timeframe feature warm-up incomplete"},
		Risk:   domain.RiskDecision{Allowed: true, Reason: "multi-timeframe feature warm-up incomplete"},
	}
	statusJSON, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		code := http.StatusOK
		body := `{"ok":true}`
		switch request.URL.Path {
		case "/status":
			body = string(statusJSON)
		case "/readyz":
			code = http.StatusServiceUnavailable
			body = `{"ready":false}`
		}
		return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}

	dir := t.TempDir()
	cfg.App.DataDirectory = dir
	if err := os.WriteFile(filepath.Join(dir, "risk_state.json"), []byte(`{"hard_halt":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("{\"kind\":\"planned_order_observe_only\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "xautbot.log")
	logData := "srv->ws: [1,[100,1,1]]\n429 ratelimit: error (11010) BITFINEX_API_SECRET=do-not-leak\n"
	if err := os.WriteFile(logPath, []byte(logData), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(cfg.Bitfinex.APIKeyEnv, "secret-key-value")
	t.Setenv(cfg.Bitfinex.APISecretEnv, "secret-value")
	t.Setenv(cfg.Bitfinex.PaperAckEnv, cfg.Bitfinex.PaperAckValue)

	report := Collect(context.Background(), cfg, Options{BaseURL: "http://bot.local", DataDir: dir, LogPath: logPath, HTTPClient: client})
	if report.Live.Status.Status == nil || report.Live.Status.Status.Features.Samples != 10 {
		t.Fatalf("live status was not collected: %+v", report.Live.Status)
	}
	if report.Log.WebSocketLinesOmitted != 1 || report.Log.RateLimitLinesSeen != 1 {
		t.Fatalf("unexpected log summary: %+v", report.Log)
	}
	if report.Recent.Events.TotalRecords != 1 {
		t.Fatalf("event records = %d, want 1", report.Recent.Events.TotalRecords)
	}
	for _, code := range []string{"not_ready", "observe_only", "feature_warmup", "regime_blocks_entries", "signal_blocks_entries", "recent_rate_limits"} {
		if !hasFinding(report.Findings, code) {
			t.Fatalf("missing finding %q: %+v", code, report.Findings)
		}
	}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, secret := range []string{"secret-key-value", "secret-value", "do-not-leak"} {
		if strings.Contains(text, secret) {
			t.Fatalf("diagnostic report leaked %q", secret)
		}
	}
	if !report.CollectorEnvironment.APIKeySet || !report.CollectorEnvironment.APISecretSet || !report.CollectorEnvironment.PaperAckMatches {
		t.Fatalf("credential presence was not captured: %+v", report.CollectorEnvironment)
	}
}

func TestBaseURLFromAddress(t *testing.T) {
	for input, want := range map[string]string{
		":8082":          "http://127.0.0.1:8082",
		"0.0.0.0:8082":   "http://127.0.0.1:8082",
		"[::]:8082":      "http://127.0.0.1:8082",
		"localhost:9000": "http://localhost:9000",
	} {
		if got := baseURLFromAddress(input); got != want {
			t.Fatalf("baseURLFromAddress(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestReadJSONLKeepsMostRecentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := readJSONL(path, 2)
	if got.TotalRecords != 3 || got.Included != 2 {
		t.Fatalf("unexpected JSONL summary: %+v", got)
	}
	last := got.Records[1].(map[string]any)
	if last["n"] != float64(3) {
		t.Fatalf("last record = %+v", last)
	}
}

func TestRedactTextHandlesShellAndJSONAssignments(t *testing.T) {
	for _, input := range []string{
		`BITFINEX_API_SECRET=plain-secret`,
		`BITFINEX_API_SECRET="quoted secret"`,
		`{"BITFINEX_API_KEY":"json-secret"}`,
	} {
		got := redactText(input)
		if strings.Contains(got, "secret") || !strings.Contains(got, "[REDACTED]") {
			t.Fatalf("redactText(%q) = %q", input, got)
		}
	}
}

func hasFinding(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
