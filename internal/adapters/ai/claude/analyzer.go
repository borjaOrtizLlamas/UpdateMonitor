// Package claude implements ports.AIAnalyzer using the Anthropic Claude API.
// It sends the raw changelog and version metadata to Claude and parses a
// structured JSON response containing a short summary and an importance flag.
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/bortizllamas/updatemonitor/internal/domain"
)

// Analyzer is the Claude-backed AI adapter.
type Analyzer struct {
	client anthropic.Client
	model  string
}

// New creates an Analyzer using the provided API key and model name.
func New(apiKey, model string) *Analyzer {
	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
	)
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	return &Analyzer{client: client, model: model}
}

// Analyze sends the version diff to Claude and returns an enriched diff with
// a short human-readable summary and an importance flag.
func (a *Analyzer) Analyze(ctx context.Context, diff *domain.VersionDiff) (*domain.VersionDiff, error) {
	prompt := buildPrompt(diff)

	msg, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 512,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude analyze: %w", err)
	}

	if len(msg.Content) == 0 {
		return diff, nil
	}

	raw := extractText(msg.Content)
	result, err := parseResponse(raw)
	if err != nil {
		// If parsing fails, use the raw response as the summary.
		diff.Summary = strings.TrimSpace(raw)
		diff.AnalyzedAt = time.Now()
		return diff, nil
	}

	diff.Summary = result.Summary
	diff.Important = result.Important
	diff.AnalyzedAt = time.Now()
	return diff, nil
}

// ---- prompt helpers --------------------------------------------------------

const systemPrompt = `You are a software update analyst. You receive information about a project version update
and return a concise, developer-friendly assessment in strict JSON format.

Your response must be valid JSON with exactly these fields:
{
  "summary": "<2-3 sentence plain-English description of what changed and why it matters>",
  "important": <true if this update contains breaking changes, security fixes, or major new features, false otherwise>
}

Be factual, concise, and helpful. Do not add any text outside the JSON object.`

func buildPrompt(diff *domain.VersionDiff) string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("Project: %s\n", diff.ProjectID))
	sb.WriteString(fmt.Sprintf("Version update: %s → %s\n\n", diff.From, diff.To))
	if diff.Changelog != "" {
		sb.WriteString("Changelog / release notes:\n")
		sb.WriteString(diff.Changelog)
	} else {
		sb.WriteString("No changelog available.")
	}
	return sb.String()
}

type analysisResponse struct {
	Summary   string `json:"summary"`
	Important bool   `json:"important"`
}

func parseResponse(raw string) (*analysisResponse, error) {
	// Strip any markdown code fences Claude may have added.
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var r analysisResponse
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func extractText(blocks []anthropic.ContentBlockUnion) string {
	var parts []string
	for _, b := range blocks {
		if tb, ok := b.AsText(); ok {
			parts = append(parts, tb.Text)
		}
	}
	return strings.Join(parts, "")
}
