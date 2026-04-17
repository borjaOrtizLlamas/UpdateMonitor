// Package nvd queries the NVD (National Vulnerability Database) REST API v2
// to retrieve CVSS severity for a given CVE ID.
// API docs: https://nvd.nist.gov/developers/vulnerabilities
package nvd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const apiURL = "https://services.nvd.nist.gov/rest/json/cves/2.0"

// Client queries NVD for CVE severity.
type Client struct {
	http   *http.Client
	apiKey string // optional — raises rate limit from 5/30s to 50/30s
	log    *slog.Logger
}

// New creates an NVD client. apiKey may be empty.
func New(apiKey string, log *slog.Logger) *Client {
	return &Client{
		http:   &http.Client{Timeout: 10 * time.Second},
		apiKey: apiKey,
		log:    log,
	}
}

// LookupSeverity returns the CVSS base severity string (CRITICAL/HIGH/MEDIUM/LOW)
// for a CVE ID. Returns "" when the CVE is not found or has no CVSS score.
// Only accepts CVE IDs (not GHSA).
func (c *Client) LookupSeverity(ctx context.Context, cveID string) (string, error) {
	if !strings.HasPrefix(strings.ToUpper(cveID), "CVE-") {
		return "", nil // NVD only handles CVE IDs
	}

	c.log.Debug("nvd: LookupSeverity called", "id", cveID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		apiURL+"?cveId="+cveID, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("apiKey", c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Warn("nvd: request failed", "id", cveID, "err", err)
		return "", fmt.Errorf("nvd request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		c.log.Debug("nvd: CVE not found", "id", cveID)
		return "", nil
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == 429 {
		c.log.Warn("nvd: rate limited", "id", cveID, "status", resp.StatusCode)
		return "", nil // don't fail — just skip severity
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		c.log.Warn("nvd: unexpected status", "id", cveID, "status", resp.StatusCode, "body", string(raw))
		return "", nil
	}

	var result nvdResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.log.Warn("nvd: failed to decode response", "id", cveID, "err", err)
		return "", nil
	}

	if result.TotalResults == 0 || len(result.Vulnerabilities) == 0 {
		c.log.Debug("nvd: no results", "id", cveID)
		return "", nil
	}

	metrics := result.Vulnerabilities[0].CVE.Metrics
	severity := ""

	// Prefer newest CVSS version first: v4 → v3.1 → v3.0 → v2
	switch {
	case len(metrics.CVSSv40) > 0:
		severity = metrics.CVSSv40[0].CVSSData.BaseSeverity
	case len(metrics.CVSSv31) > 0:
		severity = metrics.CVSSv31[0].CVSSData.BaseSeverity
	case len(metrics.CVSSv30) > 0:
		severity = metrics.CVSSv30[0].CVSSData.BaseSeverity
	case len(metrics.CVSSv2) > 0:
		// v2 doesn't have baseSeverity string; derive from baseScore
		score := metrics.CVSSv2[0].CVSSData.BaseScore
		severity = scoreToSeverity(score)
	}

	severity = strings.ToUpper(severity)
	c.log.Debug("nvd: severity resolved", "id", cveID, "severity", severity)
	return severity, nil
}

func scoreToSeverity(score float64) string {
	switch {
	case score >= 9.0:
		return "CRITICAL"
	case score >= 7.0:
		return "HIGH"
	case score >= 4.0:
		return "MEDIUM"
	case score > 0:
		return "LOW"
	default:
		return ""
	}
}

// ── NVD wire types ────────────────────────────────────────────────────────────

type nvdResponse struct {
	TotalResults    int `json:"totalResults"`
	Vulnerabilities []struct {
		CVE struct {
			ID      string     `json:"id"`
			Metrics nvdMetrics `json:"metrics"`
		} `json:"cve"`
	} `json:"vulnerabilities"`
}

type nvdMetrics struct {
	CVSSv40 []nvdCVSSEntry `json:"cvssMetricV40"`
	CVSSv31 []nvdCVSSEntry `json:"cvssMetricV31"`
	CVSSv30 []nvdCVSSEntry `json:"cvssMetricV30"`
	CVSSv2  []nvdCVSSV2    `json:"cvssMetricV2"`
}

type nvdCVSSEntry struct {
	CVSSData struct {
		BaseSeverity string  `json:"baseSeverity"`
		BaseScore    float64 `json:"baseScore"`
	} `json:"cvssData"`
}

type nvdCVSSV2 struct {
	CVSSData struct {
		BaseScore float64 `json:"baseScore"`
	} `json:"cvssData"`
}
