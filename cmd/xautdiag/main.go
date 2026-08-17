package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/diagnostic"
)

func main() {
	var configPath string
	var baseURL string
	var dataDir string
	var logPath string
	var outputPath string
	var timeout time.Duration
	var events int
	var records int
	var logLines int
	var compact bool
	flag.StringVar(&configPath, "config", "configs/config.json", "path to bot JSON configuration")
	flag.StringVar(&baseURL, "url", "", "bot monitoring base URL; inferred from config when blank")
	flag.StringVar(&dataDir, "data", "", "data directory; inferred from config when blank")
	flag.StringVar(&logPath, "log", "xautbot.log", "path to Supervisor bot log")
	flag.StringVar(&outputPath, "output", "-", "output JSON path, or - for stdout")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "timeout for each monitoring request")
	flag.IntVar(&events, "events", 100, "number of recent journal events to include")
	flag.IntVar(&records, "records", 50, "number of recent fills and trades to include")
	flag.IntVar(&logLines, "log-lines", 200, "number of relevant non-WebSocket log lines to include")
	flag.BoolVar(&compact, "compact", false, "emit compact JSON")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		fatal("load config", err)
	}
	report := diagnostic.Collect(context.Background(), cfg, diagnostic.Options{
		ConfigPath:  configPath,
		BaseURL:     baseURL,
		DataDir:     dataDir,
		LogPath:     logPath,
		HTTPTimeout: timeout,
		Events:      events,
		Records:     records,
		LogLines:    logLines,
	})
	var payload []byte
	if compact {
		payload, err = json.Marshal(report)
	} else {
		payload, err = json.MarshalIndent(report, "", "  ")
	}
	if err != nil {
		fatal("encode diagnostic report", err)
	}
	payload = append(payload, '\n')
	if outputPath == "-" {
		if _, err := os.Stdout.Write(payload); err != nil {
			fatal("write diagnostic report", err)
		}
		return
	}
	if err := writePrivateFile(outputPath, payload); err != nil {
		fatal("write diagnostic report", err)
	}
	absolute, _ := filepath.Abs(outputPath)
	fmt.Fprintf(os.Stderr, "diagnostic report: %s (%s)\n", absolute, diagnostic.FindingsSummary(report.Findings))
}

func writePrivateFile(path string, payload []byte) error {
	directory := filepath.Dir(path)
	if directory != "." {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "xautdiag: %s: %v\n", action, err)
	os.Exit(1)
}
