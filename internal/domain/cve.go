package domain

// CVE represents a known vulnerability affecting a specific version of a project.
type CVE struct {
	ID       string `json:"id"`                 // e.g. "GHSA-xxxx-xxxx-xxxx" or "CVE-2024-1234"
	Summary  string `json:"summary"`
	Severity string `json:"severity"`           // CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN
	URL      string `json:"url,omitempty"`
}

// SeverityOrder returns a numeric weight so CVEs can be sorted by severity.
func (c CVE) SeverityOrder() int {
	switch c.Severity {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	case "LOW":
		return 1
	default:
		return 0
	}
}
