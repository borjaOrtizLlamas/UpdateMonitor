// Package email implements ports.Notifier using SMTP.
package email

import (
	"bytes"
	"context"
	"fmt"
	"net/smtp"
	"strings"
	"text/template"

	"github.com/bortizllamas/updatemonitor/internal/domain"
)

// Config holds the SMTP connection parameters.
type Config struct {
	Host        string
	Port        int
	Username    string
	Password    string
	FromAddress string
}

// Notifier sends update alerts via SMTP.
type Notifier struct {
	cfg Config
}

// New creates an email notifier.
func New(cfg Config) *Notifier {
	return &Notifier{cfg: cfg}
}

// Type satisfies ports.Notifier.
func (n *Notifier) Type() string { return "email" }

// Notify sends an email for the given project update.
// The recipient is taken from the project's notification targets.
func (n *Notifier) Notify(_ context.Context, project *domain.Project) error {
	recipients := n.resolveRecipients(project)
	if len(recipients) == 0 {
		return fmt.Errorf("no email recipients configured for project %s", project.ID)
	}

	subject, body, err := n.render(project)
	if err != nil {
		return fmt.Errorf("render email: %w", err)
	}

	msg := buildMessage(n.cfg.FromAddress, recipients, subject, body)

	addr := fmt.Sprintf("%s:%d", n.cfg.Host, n.cfg.Port)
	auth := smtp.PlainAuth("", n.cfg.Username, n.cfg.Password, n.cfg.Host)

	if err := smtp.SendMail(addr, auth, n.cfg.FromAddress, recipients, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

func (n *Notifier) resolveRecipients(project *domain.Project) []string {
	var out []string
	for _, t := range project.Notifications {
		if t.Type == "email" && t.Address != "" {
			out = append(out, t.Address)
		}
	}
	return out
}

const emailTmpl = `Update available for {{ .Name }}

Project:         {{ .Name }}
Repository:      {{ .URL }}
Current version: {{ .CurrentVersion }}
Latest version:  {{ .LatestVersion }}
Important:       {{ if .UpdateImportant }}YES{{ else }}No{{ end }}

{{ if .UpdateSummary }}What changed:
{{ .UpdateSummary }}{{ end }}

---
UpdateMonitor — software version tracker`

func (n *Notifier) render(p *domain.Project) (subject, body string, err error) {
	t, err := template.New("email").Parse(emailTmpl)
	if err != nil {
		return "", "", err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, p); err != nil {
		return "", "", err
	}

	importance := ""
	if p.UpdateImportant {
		importance = " [IMPORTANT]"
	}
	subject = fmt.Sprintf("[UpdateMonitor]%s %s: %s → %s",
		importance, p.Name, p.CurrentVersion, p.LatestVersion)

	return subject, buf.String(), nil
}

func buildMessage(from string, to []string, subject, body string) []byte {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}
