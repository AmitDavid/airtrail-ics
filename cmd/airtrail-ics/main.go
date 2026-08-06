package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"airtrail-ics/internal/airtrail"
	"airtrail-ics/internal/config"
	"airtrail-ics/internal/feed"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		os.Exit(1)
	}

	client := airtrail.NewClient(cfg.AirTrailURL, cfg.AirTrailAPIKey, cfg.HTTPTimeout)
	manager, err := feed.NewManager(
		client,
		cfg.AirTrailScope,
		cfg.AirTrailUserID,
		cfg.CalendarName,
		cfg.SyncRetryCount,
		cfg.DataDir,
		cfg.AirTrailURL.String(),
		logger,
	)
	if err != nil {
		logger.Error("initialize calendar", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	scheduler := cron.New(cron.WithLocation(cfg.SyncLocation))
	if _, err := scheduler.AddFunc(cfg.SyncCron, func() {
		manager.RunCycle(ctx, "scheduled")
	}); err != nil {
		logger.Error("configure synchronization schedule", "error", err)
		os.Exit(1)
	}
	scheduler.Start()

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           manager.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info(
			"airtrail-ics started",
			"version", version,
			"commit", commit,
			"build_date", buildDate,
			"listen", cfg.ListenAddress,
			"scope", cfg.AirTrailScope,
			"schedule", cfg.SyncCron,
			"schedule_timezone", cfg.SyncTimezone,
			"retry_count", cfg.SyncRetryCount,
			"cached_feed", manager.HasSnapshot(),
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
		close(serverErrors)
	}()

	go manager.RunCycle(ctx, "startup")

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-serverErrors:
		if err != nil {
			logger.Error("HTTP server failed", "error", err)
		}
		cancel()
	}

	stopContext := scheduler.Stop()
	select {
	case <-stopContext.Done():
	case <-time.After(5 * time.Second):
		logger.Warn("scheduler did not stop within timeout")
	}

	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("HTTP server shutdown failed", "error", err)
	}
	manager.Wait()
	logger.Info("airtrail-ics stopped")
}

func runHealthcheck() int {
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://127.0.0.1:8080/readyz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "readiness returned HTTP %d\n", response.StatusCode)
		return 1
	}
	return 0
}
