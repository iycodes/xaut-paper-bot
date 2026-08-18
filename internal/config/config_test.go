package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if cfg.Risk.CapitalBaseUSD != 30_000 {
		t.Fatalf("capital = %v", cfg.Risk.CapitalBaseUSD)
	}
	if cfg.Risk.AbsoluteGrossExposure != 1 {
		t.Fatalf("gross cap = %v", cfg.Risk.AbsoluteGrossExposure)
	}
}

func TestRejectsGrossExposureAboveOne(t *testing.T) {
	cfg := Default()
	cfg.Risk.AbsoluteGrossExposure = 1.01
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestDefaultSeparatesPaperExecutionFromLiveSignalBooks(t *testing.T) {
	cfg := Default()
	if cfg.Symbols.OrderPair != "tTESTXAUT:TESTUSD" {
		t.Fatalf("paper order pair = %q", cfg.Symbols.OrderPair)
	}
	if cfg.Symbols.XAUTUSD != "tXAUT:USD" {
		t.Fatalf("signal pair = %q", cfg.Symbols.XAUTUSD)
	}
	seen := map[string]bool{}
	for _, symbol := range cfg.Symbols.All() {
		seen[symbol] = true
	}
	if !seen[cfg.Symbols.OrderPair] || !seen[cfg.Symbols.XAUTUSD] {
		t.Fatalf("symbol subscriptions omit execution or signal book: %#v", cfg.Symbols.All())
	}
}

func TestRejectsBasisWarmupLargerThanWindow(t *testing.T) {
	cfg := Default()
	cfg.Market.WarmupSamples = cfg.Market.BasisWindow + 1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected basis window validation error")
	}
}

func TestRejectsNonPositiveRESTRefreshIntervals(t *testing.T) {
	cfg := Default()
	cfg.Market.PublicTradesRefresh.Duration = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected public trade refresh validation error")
	}
	cfg = Default()
	cfg.Funding.RefreshInterval.Duration = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected funding refresh validation error")
	}
}

func TestRejectsBasisInstabilityRatioAtOrBelowBaseline(t *testing.T) {
	cfg := Default()
	cfg.Market.TransitionBasisInstability = 1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected basis instability ratio validation error")
	}
}

func TestLoadResolvesRelativeHaltFileInsideDataDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	cfg.App.DataDirectory = filepath.Join(dir, "state")
	cfg.Risk.HaltFile = "HALT"
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cfg.App.DataDirectory, "HALT")
	if loaded.Risk.HaltFile != want {
		t.Fatalf("halt file = %q, want %q", loaded.Risk.HaltFile, want)
	}
}
