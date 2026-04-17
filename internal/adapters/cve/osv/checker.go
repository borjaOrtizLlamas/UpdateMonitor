// Package osv implements ports.CVEChecker using the OSV.dev public API.
package osv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bortizllamas/updatemonitor/internal/adapters/cve/nvd"
	"github.com/bortizllamas/updatemonitor/internal/domain"
)

const (
	apiURL   = "https://api.osv.dev/v1/query"
	vulnsURL = "https://api.osv.dev/v1/vulns/"
)

// Checker queries OSV.dev for vulnerabilities and falls back to NVD for severity.
type Checker struct {
	http *http.Client
	nvd  *nvd.Client
	log  *slog.Logger
}

// New creates an OSV checker. nvdAPIKey is optional (empty = unauthenticated, 5 req/30s).
func New(log *slog.Logger) *Checker {
	return &Checker{
		http: &http.Client{Timeout: 10 * time.Second},
		nvd:  nvd.New("", log),
		log:  log,
	}
}

// Check returns CVEs for the given ecosystem/package/version combination.
func (c *Checker) Check(ctx context.Context, ecosystem, packageName, version string) ([]domain.CVE, error) {
	c.log.Debug("osv: Check called", "ecosystem", ecosystem, "package", packageName, "version", version)

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

	c.log.Debug("osv: sending query", "url", apiURL, "ecosystem", ecosystem, "package", packageName, "version", version)
	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Error("osv: query request failed", "err", err)
		return nil, fmt.Errorf("osv query: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		c.log.Error("osv: query returned non-200", "status", resp.StatusCode, "body", string(raw))
		return nil, fmt.Errorf("osv API %d: %s", resp.StatusCode, string(raw))
	}

	var result osvResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.log.Error("osv: failed to decode response", "err", err)
		return nil, fmt.Errorf("osv decode: %w", err)
	}

	cves := toCVEs(result.Vulns)
	c.log.Debug("osv: Check complete", "ecosystem", ecosystem, "package", packageName,
		"version", version, "cves_found", len(cves))
	return cves, nil
}

// LookupByID fetches details for a specific CVE or GHSA identifier from OSV.dev.
// Returns nil, nil when the ID is not found (404).
func (c *Checker) LookupByID(ctx context.Context, id string) (*domain.CVE, error) {
	c.log.Debug("osv: LookupByID called", "id", id)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, vulnsURL+id, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Error("osv: LookupByID request failed", "id", id, "err", err)
		return nil, fmt.Errorf("osv lookup %s: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		c.log.Debug("osv: ID not found in OSV database", "id", id)
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		c.log.Error("osv: LookupByID non-200 response", "id", id, "status", resp.StatusCode, "body", string(raw))
		return nil, fmt.Errorf("osv lookup %s: %d %s", id, resp.StatusCode, string(raw))
	}

	var v osvVuln
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		c.log.Error("osv: failed to decode LookupByID response", "id", id, "err", err)
		return nil, fmt.Errorf("osv decode %s: %w", id, err)
	}

	cve := domain.CVE{
		ID:      v.ID,
		Summary: v.Summary,
	}
	if cve.ID == "" {
		cve.ID = id
	}

	// ── Severity ──────────────────────────────────────────────────────────────
	// Try OSV first; fall back to NVD for CVE IDs when OSV returns nothing.
	cve.Severity = resolveSeverity(v)
	if cve.Severity == "UNKNOWN" && strings.HasPrefix(strings.ToUpper(cve.ID), "CVE-") {
		c.log.Debug("osv: severity unknown, querying NVD", "id", cve.ID)
		if s, err := c.nvd.LookupSeverity(ctx, cve.ID); err == nil && s != "" {
			cve.Severity = s
			c.log.Debug("osv: severity from NVD", "id", cve.ID, "severity", s)
		}
	}

	// ── URL ───────────────────────────────────────────────────────────────────
	// Always link to authoritative sources rather than raw OSV refs.
	upperID := strings.ToUpper(cve.ID)
	if strings.HasPrefix(upperID, "CVE-") {
		cve.URL = "https://nvd.nist.gov/vuln/detail/" + cve.ID
	} else if strings.HasPrefix(upperID, "GHSA-") {
		cve.URL = "https://github.com/advisories/" + strings.ToUpper(cve.ID)
	} else {
		cve.URL = firstWebRef(v)
	}

	c.log.Debug("osv: LookupByID success", "id", cve.ID, "severity", cve.Severity, "url", cve.URL)
	return &cve, nil
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
		Severity string `json:"severity"`
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

func cvssToSeverity(vector string) string {
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
