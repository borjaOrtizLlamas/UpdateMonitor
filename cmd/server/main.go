// Command server is the entry point for UpdateMonitor.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bortizllamas/updatemonitor/internal/adapters/ai/claude"
	osvchecker "github.com/bortizllamas/updatemonitor/internal/adapters/cve/osv"
	emailnotifier "github.com/bortizllamas/updatemonitor/internal/adapters/notifier/email"
	slacknotifier "github.com/bortizllamas/updatemonitor/internal/adapters/notifier/slack"
	s3storage "github.com/bortizllamas/updatemonitor/internal/adapters/storage/s3"
	githubtracker "github.com/bortizllamas/updatemonitor/internal/adapters/tracker/github"
	gitlabtracker "github.com/bortizllamas/updatemonitor/internal/adapters/tracker/gitlab"
	"github.com/bortizllamas/updatemonitor/internal/api"
	"github.com/bortizllamas/updatemonitor/internal/config"
	"github.com/bortizllamas/updatemonitor/internal/ports"
	"github.com/bortizllamas/updatemonitor/internal/scheduler"
	"github.com/bortizllamas/updatemonitor/internal/service"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// ── Configuration ─────────────────────────────────────────────────────────
	cfg, err := config.Load(os.Getenv("CONFIG_FILE"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Storage ───────────────────────────────────────────────────────────────
	if cfg.Storage.Bucket == "" {
		return fmt.Errorf("S3_BUCKET is required — set the env var or config file")
	}
	store, err := s3storage.New(ctx,
		cfg.Storage.Bucket,
		cfg.Storage.Region,
		cfg.Storage.Prefix,
		cfg.Storage.Endpoint,
	)
	if err != nil {
		return fmt.Errorf("init s3 storage: %w", err)
	}

	// ── Trackers ──────────────────────────────────────────────────────────────
	trackers := []ports.VersionTracker{
		githubtracker.New(cfg.GitHub.Token),
		gitlabtracker.New(cfg.GitLab.BaseURL, cfg.GitLab.Token),
	}

	// ── AI Analyzer ───────────────────────────────────────────────────────────
	var analyzer ports.AIAnalyzer
	if cfg.AI.APIKey != "" {
		analyzer = claude.New(cfg.AI.APIKey, cfg.AI.Model)
		log.Info("AI analyzer enabled", "model", cfg.AI.Model)
	} else {
		log.Warn("AI_API_KEY not set — update summaries will be skipped")
	}

	// ── CVE Checker ───────────────────────────────────────────────────────────
	// Always enabled — OSV.dev is a free public API with no key required.
	cveChecker := osvchecker.New()
	log.Info("CVE checker enabled", "provider", "osv.dev")

	// ── Notifiers ─────────────────────────────────────────────────────────────
	var notifiers []ports.Notifier
	if cfg.Notifier.Slack.WebhookURL != "" {
		notifiers = append(notifiers, slacknotifier.New(cfg.Notifier.Slack.WebhookURL))
		log.Info("Slack notifier enabled")
	}
	if cfg.Notifier.Email.SMTPHost != "" {
		notifiers = append(notifiers, emailnotifier.New(emailnotifier.Config{
			Host:        cfg.Notifier.Email.SMTPHost,
			Port:        cfg.Notifier.Email.SMTPPort,
			Username:    cfg.Notifier.Email.Username,
			Password:    cfg.Notifier.Email.Password,
			FromAddress: cfg.Notifier.Email.FromAddress,
		}))
		log.Info("Email notifier enabled")
	}

	// ── Service ───────────────────────────────────────────────────────────────
	svc := service.New(store, trackers, analyzer, cveChecker, notifiers, log)

	// ── Scheduler ─────────────────────────────────────────────────────────────
	sched := scheduler.New(cfg.Scheduler.CheckInterval,
		func(ctx context.Context) { svc.CheckAll(ctx) }, log)
	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}
	defer sched.Stop()

	// ── HTTP Server ───────────────────────────────────────────────────────────
	srv := api.New(svc, cfg)
	httpServer := &http.Server{
		Addr:         srv.Addr(),
		Handler:      srv.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("HTTP server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server", "err", err)
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down gracefully…")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return httpServer.Shutdown(shutdownCtx)
}
