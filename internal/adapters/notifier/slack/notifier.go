// Package slack implements ports.Notifier for Slack incoming webhooks.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bortizllamas/updatemonitor/internal/domain"
)

// Notifier sends update alerts to Slack via an incoming webhook URL.
type Notifier struct {
	defaultWebhook string
	http           *http.Client
}

// New creates a Slack notifier. defaultWebhook is used when a project does not
// define its own Slack target.
func New(defaultWebhook string) *Notifier {
	return &Notifier{
		defaultWebhook: defaultWebhook,
		http:           &http.Client{Timeout: 10 * time.Second},
	}
}

// Type satisfies ports.Notifier.
func (n *Notifier) Type() string { return "slack" }

// Notify sends a Slack message for the given project update.
func (n *Notifier) Notify(ctx context.Context, project *domain.Project) error {
	webhook := n.resolveWebhook(project)
	if webhook == "" {
		return fmt.Errorf("no slack webhook configured for project %s", project.ID)
	}

	payload := n.buildPayload(project)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.http.Do(req)
	if err != nil {
		return fmt.Errorf("slack webhook POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack webhook returned %d", resp.StatusCode)
	}
	return nil
}

// resolveWebhook returns the project-specific webhook or falls back to the default.
func (n *Notifier) resolveWebhook(project *domain.Project) string {
	for _, t := range project.Notifications {
		if t.Type == "slack" && t.Address != "" {
			return t.Address
		}
	}
	return n.defaultWebhook
}

// slackPayload is the incoming webhook message format.
type slackPayload struct {
	Text        string       `json:"text"`
	Attachments []attachment `json:"attachments,omitempty"`
}

type attachment struct {
	Color  string `json:"color"`
	Title  string `json:"title"`
	Text   string `json:"text"`
	Footer string `json:"footer"`
}

func (n *Notifier) buildPayload(p *domain.Project) slackPayload {
	color := "#36a64f" // green — regular update
	if p.UpdateImportant {
		color = "#e01e5a" // red — important / breaking
	}

	headerText := fmt.Sprintf(":package: *%s* has a new version: `%s` → `%s`",
		p.Name, p.CurrentVersion, p.LatestVersion)

	return slackPayload{
		Text: headerText,
		Attachments: []attachment{
			{
				Color: color,
				Title: "What changed?",
				Text:  p.UpdateSummary,
				Footer: fmt.Sprintf("UpdateMonitor • %s", p.URL),
			},
		},
	}
}
