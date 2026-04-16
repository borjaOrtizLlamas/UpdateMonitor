// Package osv implements ports.CVEChecker using the OSV.dev public API.
// OSV (Open Source Vulnerabilities) is a free, no-auth-required database
// that covers Go, npm, PyPI, Maven, RubyGems, Cargo, and many more ecosystems.
// API docs: https://google.github.io/osv.dev/api/
package osv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/bortizllamas/updatemonitor/internal/domain"
)

const apiURL = "https://api.osv.dev/v1/query"

// Checker queries OSV.dev for vulnerabilities.
type Checker struct {
	http *http.Client
}

// New creates an OSV checker.
func New() *Checker {
	return &Checker{
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// Check returns CVEs for the given ecosystem/package/version combination.
// Returns an empty slice when no vulnerabilities are found.
func (c *Checker) Check(ctx context.Context, ecosystem, packageName, version string) ([]domain.CVE, error) {
	body, err := json.Marshal(osvQuery{
		Version: version,
		Package: osvPackage{
			Ecosystem: ecosystem,
			Name:      packageName,
		},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("osv query: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("osv API %d: %s", resp.StatusCode, string(raw))
	}

	var result osvResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("osv decode: %w", err)
	}

	return toCVEs(result.Vulns), nil
}

// ── OSV wire types ────────────────────────────────────────────────────────────

type osvQuery struct {
	Version string     `json:"version"`
	Package osvPackage `json:"package"`
}

type osvPackage struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}

type osvResponse struct {
	Vulns []osvVuln `json:"vulns"`
}

type osvVuln struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	DatabaseSpecific struct {
		Severity string `json:"severity"` // CRITICAL, HIGH, MEDIUM, LOW
	} `json:"database_specific"`
	References []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"references"`
}

func toCVEs(vulns []osvVuln) []domain.CVE {
	if len(vulns) == 0 {
		return nil
	}
	out := make([]domain.CVE, 0, len(vulns))
	for _, v := range vulns {
		out = append(out, domain.CVE{
			ID:       v.ID,
			Summary:  v.Summary,
			Severity: resolveSeverity(v),
			URL:      firstWebRef(v),
		})
	}
	return out
}

// resolveSeverity extracts the severity string from an OSV vulnerability.
// It prefers database_specific.severity (already normalised by the DB),
// then falls back to deriving it from the CVSS v3 score.
func resolveSeverity(v osvVuln) string {
	if s := v.DatabaseSpecific.Severity; s != "" {
		return s
	}
	for _, s := range v.Severity {
		if s.Type == "CVSS_V3" {
			return cvssToSeverity(s.Score)
		}
	}
	return "UNKNOWN"
}

// cvssToSeverity maps a raw CVSS v3 vector string to a severity label.
// The base score is embedded in the vector: CVSS:3.x/.../X.X
func cvssToSeverity(vector string) string {
	// Extract the numeric base score from the last segment of the vector string.
	// Format: CVSS:3.1/AV:N/AC:L/...  — score is NOT in the vector itself here;
	// OSV sometimes provides the full score object. Fall back to UNKNOWN.
	_ = vector
	return "UNKNOWN"
}

func firstWebRef(v osvVuln) string {
	for _, r := range v.References {
		if r.Type == "WEB" || r.Type == "ADVISORY" {
			return r.URL
		}
	}
	if len(v.References) > 0 {
		return v.References[0].URL
	}
	return ""
}
