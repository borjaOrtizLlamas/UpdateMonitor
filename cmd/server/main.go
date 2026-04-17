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
	level := slog.LevelDebug // default to debug so everything is visible
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		switch v {
		case "info", "INFO":
			level = slog.LevelInfo
		case "warn", "WARN":
			level = slog.LevelWarn
		case "error", "ERROR":
			level = slog.LevelError
		}
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
	slog.SetDefault(log)
	log.Info("logger initialised", "level", level.String())

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	log.Debug("loading configuration")

	// ── Configuration ─────────────────────────────────────────────────────────
	cfg, err := config.Load(os.Getenv("CONFIG_FILE"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Log effective config — mask secrets
	log.Info("config loaded",
		"server.port", cfg.Server.Port,
		"server.admin_key_set", cfg.Server.AdminKey != "",
		"storage.bucket", cfg.Storage.Bucket,
		"storage.region", cfg.Storage.Region,
		"storage.prefix", cfg.Storage.Prefix,
		"storage.endpoint", cfg.Storage.Endpoint,
		"github.token_set", cfg.GitHub.Token != "",
		"gitlab.token_set", cfg.GitLab.Token != "",
		"gitlab.base_url", cfg.GitLab.BaseURL,
		"ai.provider", cfg.AI.Provider,
		"ai.model", cfg.AI.Model,
		"ai.api_key_set", cfg.AI.APIKey != "",
		"scheduler.interval", cfg.Scheduler.CheckInterval,
		"notifier.slack_set", cfg.Notifier.Slack.WebhookURL != "",
		"notifier.email_host", cfg.Notifier.Email.SMTPHost,
	)

	if cfg.Storage.Bucket == "" {
		log.Error("S3_BUCKET is empty — cannot start without a bucket")
		return fmt.Errorf("S3_BUCKET is required — set the env var or config file")
	}
	if cfg.Storage.Region == "" {
		log.Error("S3_REGION is empty — SDK may fail to resolve endpoint")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Storage ───────────────────────────────────────────────────────────────
	log.Debug("initialising S3 storage",
		"bucket", cfg.Storage.Bucket,
		"region", cfg.Storage.Region,
		"prefix", cfg.Storage.Prefix,
		"endpoint", cfg.Storage.Endpoint,
	)
	store, err := s3storage.New(ctx,
		cfg.Storage.Bucket,
		cfg.Storage.Region,
		cfg.Storage.Prefix,
		cfg.Storage.Endpoint,
		log,
	)
	if err != nil {
		log.Error("failed to init S3 storage", "err", err)
		return fmt.Errorf("init s3 storage: %w", err)
	}
	log.Info("S3 storage initialised",
		"bucket", cfg.Storage.Bucket,
		"region", cfg.Storage.Region,
		"endpoint_override", cfg.Storage.Endpoint != "",
	)

	// Smoke-test: try to read the project list so we catch credential/bucket
	// errors at startup rather than on the first API call.
	log.Debug("running S3 smoke-test read")
	if _, err := store.LoadProjects(ctx); err != nil {
		log.Error("S3 smoke-test failed — storage is misconfigured",
			"bucket", cfg.Storage.Bucket,
			"region", cfg.Storage.Region,
			"err", err,
		)
		return fmt.Errorf("s3 smoke-test: %w", err)
	}
	log.Info("S3 smoke-test passed — bucket is reachable")

	// ── Trackers ──────────────────────────────────────────────────────────────
	log.Debug("initialising trackers")
	trackers := []ports.VersionTracker{
		githubtracker.New(cfg.GitHub.Token, log),
		gitlabtracker.New(cfg.GitLab.BaseURL, cfg.GitLab.Token, log),
	}
	log.Info("trackers initialised", "count", len(trackers))

	// ── AI Analyzer ───────────────────────────────────────────────────────────
	var analyzer ports.AIAnalyzer
	if cfg.AI.APIKey != "" {
		analyzer = claude.New(cfg.AI.APIKey, cfg.AI.Model)
		log.Info("AI analyzer enabled", "model", cfg.AI.Model)
	} else {
		log.Warn("AI_API_KEY not set — update summaries will be skipped")
	}

	// ── CVE Checker ───────────────────────────────────────────────────────────
	cveChecker := osvchecker.New(log)
	log.Info("CVE checker enabled", "provider", "osv.dev")

	// ── Notifiers ─────────────────────────────────────────────────────────────
	var notifiers []ports.Notifier
	if cfg.Notifier.Slack.WebhookURL != "" {
		notifiers = append(notifiers, slacknotifier.New(cfg.Notifier.Slack.WebhookURL))
		log.Info("Slack notifier enabled")
	} else {
		log.Debug("Slack notifier disabled — SLACK_WEBHOOK_URL not set")
	}
	if cfg.Notifier.Email.SMTPHost != "" {
		notifiers = append(notifiers, emailnotifier.New(emailnotifier.Config{
			Host:        cfg.Notifier.Email.SMTPHost,
			Port:        cfg.Notifier.Email.SMTPPort,
			Username:    cfg.Notifier.Email.Username,
			Password:    cfg.Notifier.Email.Password,
			FromAddress: cfg.Notifier.Email.FromAddress,
		}))
		log.Info("Email notifier enabled", "host", cfg.Notifier.Email.SMTPHost)
	} else {
		log.Debug("Email notifier disabled — SMTP_HOST not set")
	}

	// ── Service ───────────────────────────────────────────────────────────────
	log.Debug("initialising project service")
	svc := service.New(store, trackers, analyzer, cveChecker, notifiers, log)
	log.Info("project service initialised")

	// ── Scheduler ─────────────────────────────────────────────────────────────
	log.Debug("initialising scheduler", "interval", cfg.Scheduler.CheckInterval)
	sched := scheduler.New(cfg.Scheduler.CheckInterval,
		func(ctx context.Context) { svc.CheckAll(ctx) }, log)
	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}
	defer sched.Stop()

	// ── HTTP Server ───────────────────────────────────────────────────────────
	srv := api.New(svc, cfg, log)
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
			log.Error("http server error", "err", err)
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("shutdown signal received", "signal", sig.String())
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return httpServer.Shutdown(shutdownCtx)
}
