package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"xaut-paper-bot/internal/app"
	"xaut-paper-bot/internal/config"
	bitfinexexchange "xaut-paper-bot/internal/exchange/bitfinex"
	"xaut-paper-bot/internal/journal"
	"xaut-paper-bot/internal/monitor"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "configs/config.json", "path to JSON configuration")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}
	j, err := journal.New(cfg.App.DataDirectory)
	if err != nil {
		logger.Error("open journal", "error", err)
		os.Exit(1)
	}
	defer j.Close()

	started := time.Now().UTC()
	store := monitor.NewStore(started)
	staleAfter := 3 * cfg.App.TickInterval.Duration
	if staleAfter < 30*time.Second {
		staleAfter = 30 * time.Second
	}
	server := monitor.New(cfg.App.HTTPAddress, store, staleAfter)
	if err := server.Start(); err != nil {
		logger.Error("start status server", "address", cfg.App.HTTPAddress, "error", err)
		os.Exit(1)
	}
	logger.Info("status server listening", "address", cfg.App.HTTPAddress)
	go func() {
		if err := <-server.Errors(); err != nil {
			logger.Error("status server stopped", "error", err)
		}
	}()
	defer server.Shutdown()

	ex := bitfinexexchange.New(cfg)
	bot, err := app.New(cfg, ex, j, store, logger)
	if err != nil {
		logger.Error("construct application", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting XAUT bot", "paper_only", bitfinexexchange.PaperOnlyBuild, "observe_only", cfg.App.ObserveOnly, "capital_usd", cfg.Risk.CapitalBaseUSD)
	if err := bot.Run(ctx); err != nil {
		logger.Error("bot stopped", "error", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger.Info("bot stopped cleanly")
}
