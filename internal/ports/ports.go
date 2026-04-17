// Package ports defines all interface contracts (hexagonal architecture).
// Adapters in internal/adapters implement these interfaces.
package ports

import (
	"context"

	"github.com/bortizllamas/updatemonitor/internal/domain"
)

// Storage is the persistence port. The default implementation uses S3.
type Storage interface {
	// LoadProjects returns the current project store from the backend.
	LoadProjects(ctx context.Context) (*domain.ProjectStore, error)

	// SaveProjects persists the project store to the backend.
	SaveProjects(ctx context.Context, store *domain.ProjectStore) error
}

// VersionTracker is the provider port. One implementation per platform.
type VersionTracker interface {
	// LatestVersion returns the most recent tag/release for the given project.
	LatestVersion(ctx context.Context, owner, repo string) (*domain.VersionInfo, error)

	// Changelog returns the release notes or commit messages between two tags.
	// fromTag and toTag are inclusive — the result covers all changes the user
	// has not yet applied (i.e. currentVersion → latestVersion).
	Changelog(ctx context.Context, owner, repo, fromTag, toTag string) (string, error)

	// AllChanges returns every release body and commit message between fromTag
	// (exclusive) and toTag (inclusive). Used to scan the full update range for
	// CVE mentions in release announcements and commit messages.
	AllChanges(ctx context.Context, owner, repo, fromTag, toTag string) ([]domain.ReleaseInfo, error)

	// Platform returns which hosting platform this tracker handles.
	Platform() domain.Platform
}

// AIAnalyzer is the AI integration port.
type AIAnalyzer interface {
	// Analyze takes a version diff (raw changelog) and returns a human-readable
	// summary and an importance flag.
	Analyze(ctx context.Context, diff *domain.VersionDiff) (*domain.VersionDiff, error)
}

// Notifier is the notification port. One implementation per channel.
type Notifier interface {
	// Notify sends an update notification for the given project.
	Notify(ctx context.Context, project *domain.Project) error

	// Type returns the notification channel identifier.
	Type() string
}

// CVEChecker is the vulnerability-scanning port.
// The default implementation queries the OSV.dev public API.
type CVEChecker interface {
	// Check returns known CVEs for the given ecosystem, package, and version.
	// Returns an empty slice (not an error) when no vulnerabilities are found.
	Check(ctx context.Context, ecosystem, packageName, version string) ([]domain.CVE, error)

	// LookupByID fetches details for a specific CVE or GHSA identifier.
	// Returns nil, nil when the ID is not found in the database.
	LookupByID(ctx context.Context, id string) (*domain.CVE, error)
}
