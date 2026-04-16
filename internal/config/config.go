// Package config loads and exposes application configuration.
// Values are read from environment variables, falling back to a JSON file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// Config is the root configuration object for the application.
type Config struct {
	Server    ServerConfig    `json:"server"`
	Storage   StorageConfig   `json:"storage"`
	GitHub    GitHubConfig    `json:"github"`
	GitLab    GitLabConfig    `json:"gitlab"`
	AI        AIConfig        `json:"ai"`
	Notifier  NotifierConfig  `json:"notifier"`
	Auth      AuthConfig      `json:"auth"`
	Scheduler SchedulerConfig `json:"scheduler"`
}

type ServerConfig struct {
	Port     int    `json:"port"`
	BasePath string `json:"base_path"`
	// Admin API key used to protect configuration endpoints.
	// Set via SERVER_ADMIN_KEY env var.
	AdminKey string `json:"admin_key"`
}

type StorageConfig struct {
	// Provider is always "s3" for now. Kept for future extensibility.
	Provider string `json:"provider"`
	Bucket   string `json:"bucket"`
	Region   string `json:"region"`
	// S3 key prefix, e.g. "updatemonitor/"
	Prefix string `json:"prefix"`
	// Optional: set to use localstack or MinIO for development
	Endpoint string `json:"endpoint"`
}

type GitHubConfig struct {
	// Personal access token — optional, raises rate limits.
	Token string `json:"token"`
}

type GitLabConfig struct {
	BaseURL string `json:"base_url"` // defaults to https://gitlab.com
	Token   string `json:"token"`
}

type AIConfig struct {
	// Provider: "claude" (default)
	Provider string `json:"provider"`
	// APIKey for the AI provider — set via AI_API_KEY env var.
	APIKey string `json:"api_key"`
	// Model to use, e.g. "claude-sonnet-4-6"
	Model string `json:"model"`
}

type NotifierConfig struct {
	Email EmailConfig `json:"email"`
	Slack SlackConfig `json:"slack"`
}

type EmailConfig struct {
	SMTPHost    string `json:"smtp_host"`
	SMTPPort    int    `json:"smtp_port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	FromAddress string `json:"from_address"`
}

type SlackConfig struct {
	// Default webhook URL; per-project overrides are stored on the project itself.
	WebhookURL string `json:"webhook_url"`
}

type AuthConfig struct {
	// Enabled controls whether admin endpoints require the admin key.
	Enabled bool `json:"enabled"`
	// Future: KeycloakURL, Realm, ClientID, etc.
}

type SchedulerConfig struct {
	// CheckInterval is a cron expression for version checks (default: every 30 min).
	CheckInterval string `json:"check_interval"`
}

// Load reads configuration from a JSON file, then overlays environment variables.
// path may be empty — in that case only env vars are used.
func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		f, err := os.Open(path)
		if err == nil {
			defer f.Close()
			if err := json.NewDecoder(f).Decode(cfg); err != nil {
				return nil, fmt.Errorf("parsing config file: %w", err)
			}
		}
	}

	overlayFromEnv(cfg)
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Port:     8080,
			BasePath: "/",
		},
		Storage: StorageConfig{
			Provider: "s3",
			Region:   "us-east-1",
			Prefix:   "updatemonitor/",
		},
		GitLab: GitLabConfig{
			BaseURL: "https://gitlab.com",
		},
		AI: AIConfig{
			Provider: "claude",
			Model:    "claude-sonnet-4-6",
		},
		Auth: AuthConfig{
			Enabled: true,
		},
		Scheduler: SchedulerConfig{
			CheckInterval: "*/30 * * * *",
		},
	}
}

func overlayFromEnv(cfg *Config) {
	if v := os.Getenv("SERVER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = p
		}
	}
	if v := os.Getenv("SERVER_ADMIN_KEY"); v != "" {
		cfg.Server.AdminKey = v
	}

	if v := os.Getenv("S3_BUCKET"); v != "" {
		cfg.Storage.Bucket = v
	}
	if v := os.Getenv("S3_REGION"); v != "" {
		cfg.Storage.Region = v
	}
	if v := os.Getenv("S3_PREFIX"); v != "" {
		cfg.Storage.Prefix = v
	}
	if v := os.Getenv("S3_ENDPOINT"); v != "" {
		cfg.Storage.Endpoint = v
	}

	if v := os.Getenv("GITHUB_TOKEN"); v != "" {
		cfg.GitHub.Token = v
	}
	if v := os.Getenv("GITLAB_TOKEN"); v != "" {
		cfg.GitLab.Token = v
	}
	if v := os.Getenv("GITLAB_BASE_URL"); v != "" {
		cfg.GitLab.BaseURL = v
	}

	if v := os.Getenv("AI_API_KEY"); v != "" {
		cfg.AI.APIKey = v
	}
	if v := os.Getenv("AI_MODEL"); v != "" {
		cfg.AI.Model = v
	}

	if v := os.Getenv("SLACK_WEBHOOK_URL"); v != "" {
		cfg.Notifier.Slack.WebhookURL = v
	}
	if v := os.Getenv("SMTP_HOST"); v != "" {
		cfg.Notifier.Email.SMTPHost = v
	}
	if v := os.Getenv("SMTP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Notifier.Email.SMTPPort = p
		}
	}
	if v := os.Getenv("SMTP_USER"); v != "" {
		cfg.Notifier.Email.Username = v
	}
	if v := os.Getenv("SMTP_PASSWORD"); v != "" {
		cfg.Notifier.Email.Password = v
	}
	if v := os.Getenv("SMTP_FROM"); v != "" {
		cfg.Notifier.Email.FromAddress = v
	}

	if v := os.Getenv("CHECK_INTERVAL"); v != "" {
		cfg.Scheduler.CheckInterval = v
	}
}
