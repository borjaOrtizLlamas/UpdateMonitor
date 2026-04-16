package domain

import "time"

// Platform represents a supported source hosting provider.
type Platform string

const (
	PlatformGitHub Platform = "github"
	PlatformGitLab Platform = "gitlab"
)

// Project is the central domain entity representing a tracked software project.
type Project struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	Platform Platform `json:"platform"`
	Owner    string   `json:"owner"`
	Repo     string   `json:"repo"`

	// Version tracking
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`

	// AI-generated context
	UpdateSummary   string `json:"update_summary,omitempty"`
	UpdateImportant bool   `json:"update_important,omitempty"`

	// Snooze — suppress alerts for a specific version the user knowingly skips.
	SnoozedUntilVersion string    `json:"snoozed_until_version,omitempty"`
	SnoozedAt           time.Time `json:"snoozed_at,omitempty"`

	// CVE vulnerability data.
	// Populated when Ecosystem + PackageName are set and OSV.dev returns results.
	Ecosystem   string `json:"ecosystem,omitempty"`    // e.g. "Go", "npm", "PyPI"
	PackageName string `json:"package_name,omitempty"` // e.g. "github.com/foo/bar"
	CVEs        []CVE  `json:"cves,omitempty"`
	HasCVEs     bool   `json:"has_cves"`

	// Notification settings
	Notifications []NotificationTarget `json:"notifications,omitempty"`

	// Metadata
	LastChecked time.Time `json:"last_checked"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// IsSnoozed returns true if the project is currently suppressing alerts
// for its latest known version.
func (p *Project) IsSnoozed() bool {
	return p.SnoozedUntilVersion != "" && p.SnoozedUntilVersion == p.LatestVersion
}

// NotificationTarget defines a channel to notify when an update is detected.
type NotificationTarget struct {
	Type    string            `json:"type"`    // "email", "slack"
	Address string            `json:"address"` // email address or Slack webhook URL
	Options map[string]string `json:"options,omitempty"`
}

// ProjectStore is the in-memory collection of all tracked projects.
type ProjectStore struct {
	Projects  []*Project `json:"projects"`
	UpdatedAt time.Time  `json:"updated_at"`
}
