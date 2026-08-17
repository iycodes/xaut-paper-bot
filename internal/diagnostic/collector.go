package diagnostic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

const SchemaVersion = 1

var secretAssignment = regexp.MustCompile(`(?i)"?(BITFINEX_API_KEY|BITFINEX_API_SECRET|BFX_PAPER_TRADING_ACK)"?\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,}]+)`)

type Options struct {
	ConfigPath  string
	BaseURL     string
	DataDir     string
	LogPath     string
	HTTPTimeout time.Duration
	Events      int
	Records     int
	LogLines    int
	HTTPClient  *http.Client
}

type Report struct {
	SchemaVersion        int                   `json:"schema_version"`
	CollectedAt          time.Time             `json:"collected_at"`
	Safety               Safety                `json:"safety"`
	Host                 Host                  `json:"host"`
	Build                Build                 `json:"build"`
	Inputs               Inputs                `json:"inputs"`
	CollectorEnvironment CredentialEnvironment `json:"collector_credential_environment"`
	Config               config.Config         `json:"config"`
	Live                 Live                  `json:"live"`
	StateFiles           map[string]JSONFile   `json:"state_files"`
	Recent               Recent                `json:"recent"`
	Log                  LogTail               `json:"log"`
	Findings             []Finding             `json:"findings"`
	Warnings             []string              `json:"collection_warnings,omitempty"`
}

type Safety struct {
	EnvironmentValuesCollected  bool `json:"environment_values_collected"`
	EnvFileRead                 bool `json:"env_file_read"`
	SecretsIntentionallyOmitted bool `json:"secrets_intentionally_omitted"`
}

type Host struct {
	Hostname         string `json:"hostname"`
	OperatingSystem  string `json:"operating_system"`
	Architecture     string `json:"architecture"`
	GoVersion        string `json:"go_version"`
	WorkingDirectory string `json:"working_directory"`
	ProcessID        int    `json:"collector_process_id"`
}

type Build struct {
	Module      string `json:"module,omitempty"`
	GoVersion   string `json:"go_version,omitempty"`
	Revision    string `json:"revision,omitempty"`
	RevisionAt  string `json:"revision_at,omitempty"`
	VCSModified string `json:"vcs_modified,omitempty"`
}

type Inputs struct {
	ConfigPath string `json:"config_path"`
	BaseURL    string `json:"base_url"`
	DataDir    string `json:"data_directory"`
	LogPath    string `json:"log_path"`
}

type CredentialEnvironment struct {
	APIKeyVariable    string `json:"api_key_variable"`
	APIKeySet         bool   `json:"api_key_set"`
	APISecretVariable string `json:"api_secret_variable"`
	APISecretSet      bool   `json:"api_secret_set"`
	PaperAckVariable  string `json:"paper_ack_variable"`
	PaperAckSet       bool   `json:"paper_ack_set"`
	PaperAckMatches   bool   `json:"paper_ack_matches"`
}

type Live struct {
	Status    StatusProbe `json:"status"`
	Health    Probe       `json:"health"`
	Readiness Probe       `json:"readiness"`
}

type StatusProbe struct {
	URL        string                `json:"url"`
	HTTPStatus int                   `json:"http_status,omitempty"`
	LatencyMS  int64                 `json:"latency_ms"`
	Error      string                `json:"error,omitempty"`
	Status     *domain.RuntimeStatus `json:"body,omitempty"`
}

type Probe struct {
	URL        string `json:"url"`
	HTTPStatus int    `json:"http_status,omitempty"`
	LatencyMS  int64  `json:"latency_ms"`
	Error      string `json:"error,omitempty"`
	Body       any    `json:"body,omitempty"`
}

type JSONFile struct {
	Path       string    `json:"path"`
	Exists     bool      `json:"exists"`
	SizeBytes  int64     `json:"size_bytes,omitempty"`
	ModifiedAt time.Time `json:"modified_at,omitempty"`
	Error      string    `json:"error,omitempty"`
	Content    any       `json:"content,omitempty"`
}

type JSONL struct {
	Path         string `json:"path"`
	Exists       bool   `json:"exists"`
	TotalRecords int    `json:"total_records"`
	Included     int    `json:"included_records"`
	DecodeErrors int    `json:"decode_errors"`
	Error        string `json:"error,omitempty"`
	Records      []any  `json:"records,omitempty"`
}

type Recent struct {
	Events JSONL `json:"events"`
	Fills  JSONL `json:"fills"`
	Trades JSONL `json:"trades"`
}

type LogTail struct {
	Path                  string   `json:"path"`
	Exists                bool     `json:"exists"`
	SizeBytes             int64    `json:"size_bytes,omitempty"`
	BytesInspected        int64    `json:"bytes_inspected,omitempty"`
	WebSocketLinesOmitted int      `json:"websocket_lines_omitted"`
	RateLimitLinesSeen    int      `json:"rate_limit_lines_seen"`
	Error                 string   `json:"error,omitempty"`
	Lines                 []string `json:"lines,omitempty"`
}

func Collect(ctx context.Context, cfg config.Config, options Options) Report {
	options = normalizeOptions(cfg, options)
	report := Report{
		SchemaVersion: SchemaVersion,
		CollectedAt:   time.Now().UTC(),
		Safety: Safety{
			EnvironmentValuesCollected:  false,
			EnvFileRead:                 false,
			SecretsIntentionallyOmitted: true,
		},
		Host:                 collectHost(),
		Build:                collectBuild(),
		Inputs:               Inputs{ConfigPath: options.ConfigPath, BaseURL: options.BaseURL, DataDir: options.DataDir, LogPath: options.LogPath},
		CollectorEnvironment: collectCredentialEnvironment(cfg),
		Config:               cfg,
		StateFiles:           map[string]JSONFile{},
	}

	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: options.HTTPTimeout}
	}
	report.Live.Status = fetchStatus(ctx, client, options.BaseURL+"/status")
	report.Live.Health = fetchProbe(ctx, client, options.BaseURL+"/healthz")
	report.Live.Readiness = fetchProbe(ctx, client, options.BaseURL+"/readyz")

	for _, name := range []string{"basis_state.json", "risk_state.json", "position_state.json", "performance_state.json"} {
		report.StateFiles[name] = readJSONFile(filepath.Join(options.DataDir, name))
	}
	report.StateFiles["halt_file"] = inspectHaltFile(cfg.Risk.HaltFile)
	report.Recent = Recent{
		Events: readJSONL(filepath.Join(options.DataDir, "events.jsonl"), options.Events),
		Fills:  readJSONL(filepath.Join(options.DataDir, "fills.jsonl"), options.Records),
		Trades: readJSONL(filepath.Join(options.DataDir, "trades.jsonl"), options.Records),
	}
	report.Log = readLogTail(options.LogPath, options.LogLines)
	report.Warnings = collectionWarnings(report)
	report.Findings = deriveFindings(report)
	return report
}

func normalizeOptions(cfg config.Config, options Options) Options {
	if options.ConfigPath == "" {
		options.ConfigPath = "configs/config.json"
	}
	if options.BaseURL == "" {
		options.BaseURL = baseURLFromAddress(cfg.App.HTTPAddress)
	}
	options.BaseURL = strings.TrimRight(options.BaseURL, "/")
	if options.DataDir == "" {
		options.DataDir = cfg.App.DataDirectory
	}
	if options.LogPath == "" {
		options.LogPath = "xautbot.log"
	}
	if options.HTTPTimeout <= 0 {
		options.HTTPTimeout = 5 * time.Second
	}
	if options.Events <= 0 {
		options.Events = 100
	}
	if options.Records <= 0 {
		options.Records = 50
	}
	if options.LogLines <= 0 {
		options.LogLines = 200
	}
	options.ConfigPath = absolutePath(options.ConfigPath)
	options.DataDir = absolutePath(options.DataDir)
	options.LogPath = absolutePath(options.LogPath)
	return options
}

func baseURLFromAddress(address string) string {
	address = strings.TrimSpace(address)
	if strings.HasPrefix(address, "http://") || strings.HasPrefix(address, "https://") {
		return strings.TrimRight(address, "/")
	}
	if strings.HasPrefix(address, ":") {
		return "http://127.0.0.1" + address
	}
	if address == "" {
		return "http://127.0.0.1:8082"
	}
	if strings.HasPrefix(address, "0.0.0.0:") {
		return "http://127.0.0.1:" + strings.TrimPrefix(address, "0.0.0.0:")
	}
	if strings.HasPrefix(address, "[::]:") {
		return "http://127.0.0.1:" + strings.TrimPrefix(address, "[::]:")
	}
	return "http://" + address
}

func fetchStatus(ctx context.Context, client *http.Client, url string) StatusProbe {
	started := time.Now()
	probe := StatusProbe{URL: url}
	body, status, err := request(ctx, client, url)
	probe.LatencyMS = time.Since(started).Milliseconds()
	probe.HTTPStatus = status
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	var runtimeStatus domain.RuntimeStatus
	if err := json.Unmarshal(body, &runtimeStatus); err != nil {
		probe.Error = "decode status JSON: " + err.Error()
		return probe
	}
	probe.Status = &runtimeStatus
	return probe
}

func fetchProbe(ctx context.Context, client *http.Client, url string) Probe {
	started := time.Now()
	probe := Probe{URL: url}
	body, status, err := request(ctx, client, url)
	probe.LatencyMS = time.Since(started).Milliseconds()
	probe.HTTPStatus = status
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		probe.Body = string(body)
	} else {
		probe.Body = decoded
	}
	return probe
}

func request(ctx context.Context, client *http.Client, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func readJSONFile(path string) JSONFile {
	result := JSONFile{Path: absolutePath(path)}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Exists = true
	result.SizeBytes = info.Size()
	result.ModifiedAt = info.ModTime().UTC()
	data, err := os.ReadFile(path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		result.Error = "decode JSON: " + err.Error()
		return result
	}
	result.Content = decoded
	return result
}

func inspectHaltFile(path string) JSONFile {
	result := JSONFile{Path: absolutePath(path)}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Exists = true
	result.SizeBytes = info.Size()
	result.ModifiedAt = info.ModTime().UTC()
	return result
}

func readJSONL(path string, limit int) JSONL {
	result := JSONL{Path: absolutePath(path)}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer file.Close()
	result.Exists = true
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	rolling := make([]any, 0, limit)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		result.TotalRecords++
		var decoded any
		if err := json.Unmarshal(line, &decoded); err != nil {
			result.DecodeErrors++
			decoded = map[string]any{"decode_error": err.Error(), "raw": redactText(string(line))}
		}
		if len(rolling) < limit {
			rolling = append(rolling, decoded)
		} else {
			copy(rolling, rolling[1:])
			rolling[len(rolling)-1] = decoded
		}
	}
	if err := scanner.Err(); err != nil {
		result.Error = err.Error()
	}
	result.Records = rolling
	result.Included = len(rolling)
	return result
}

func readLogTail(path string, limit int) LogTail {
	result := LogTail{Path: absolutePath(path)}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Exists = true
	result.SizeBytes = info.Size()
	const maximumInspection = int64(32 << 20)
	readSize := info.Size()
	if readSize > maximumInspection {
		readSize = maximumInspection
	}
	offset := info.Size() - readSize
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		result.Error = err.Error()
		return result
	}
	data, err := io.ReadAll(file)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.BytesInspected = int64(len(data))
	lines := strings.Split(string(data), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	relevant := make([]string, 0, limit)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "srv->ws:") || strings.Contains(lower, "ws->srv:") {
			result.WebSocketLinesOmitted++
			continue
		}
		if isRateLimitText(lower) {
			result.RateLimitLinesSeen++
		}
		trimmed = redactText(trimmed)
		if len(relevant) < limit {
			relevant = append(relevant, trimmed)
		} else {
			copy(relevant, relevant[1:])
			relevant[len(relevant)-1] = trimmed
		}
	}
	result.Lines = relevant
	return result
}

func collectCredentialEnvironment(cfg config.Config) CredentialEnvironment {
	key, keySet := os.LookupEnv(cfg.Bitfinex.APIKeyEnv)
	secret, secretSet := os.LookupEnv(cfg.Bitfinex.APISecretEnv)
	ack, ackSet := os.LookupEnv(cfg.Bitfinex.PaperAckEnv)
	return CredentialEnvironment{
		APIKeyVariable:    cfg.Bitfinex.APIKeyEnv,
		APIKeySet:         keySet && strings.TrimSpace(key) != "",
		APISecretVariable: cfg.Bitfinex.APISecretEnv,
		APISecretSet:      secretSet && strings.TrimSpace(secret) != "",
		PaperAckVariable:  cfg.Bitfinex.PaperAckEnv,
		PaperAckSet:       ackSet && ack != "",
		PaperAckMatches:   ackSet && ack == cfg.Bitfinex.PaperAckValue,
	}
}

func collectHost() Host {
	hostname, _ := os.Hostname()
	workingDirectory, _ := os.Getwd()
	return Host{Hostname: hostname, OperatingSystem: runtime.GOOS, Architecture: runtime.GOARCH, GoVersion: runtime.Version(), WorkingDirectory: workingDirectory, ProcessID: os.Getpid()}
}

func collectBuild() Build {
	result := Build{}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return result
	}
	result.Module = info.Main.Path
	result.GoVersion = info.GoVersion
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			result.Revision = setting.Value
		case "vcs.time":
			result.RevisionAt = setting.Value
		case "vcs.modified":
			result.VCSModified = setting.Value
		}
	}
	return result
}

func collectionWarnings(report Report) []string {
	warnings := []string{}
	if report.Live.Status.Error != "" {
		warnings = append(warnings, "live status could not be collected: "+report.Live.Status.Error)
	}
	for name, state := range report.StateFiles {
		if state.Error != "" {
			warnings = append(warnings, name+": "+state.Error)
		}
	}
	for name, stream := range map[string]JSONL{"events": report.Recent.Events, "fills": report.Recent.Fills, "trades": report.Recent.Trades} {
		if stream.Error != "" {
			warnings = append(warnings, name+": "+stream.Error)
		}
	}
	if report.Log.Error != "" {
		warnings = append(warnings, "log: "+report.Log.Error)
	}
	return warnings
}

func absolutePath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func redactText(value string) string {
	return secretAssignment.ReplaceAllString(value, "${1}=[REDACTED]")
}

func isRateLimitText(value string) bool {
	return strings.Contains(value, "429") || strings.Contains(value, "ratelimit") || strings.Contains(value, "rate limit") || strings.Contains(value, "11010")
}

func ParseDurationFlag(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", value, err)
	}
	return duration, nil
}

func ParsePositiveInt(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("value must be a positive integer: %q", value)
	}
	return n, nil
}
