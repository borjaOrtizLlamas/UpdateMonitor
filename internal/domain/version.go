package domain

import "time"

// VersionInfo holds the result of a version check for a project.
type VersionInfo struct {
	Tag         string    `json:"tag"`
	PublishedAt time.Time `json:"published_at"`
	ReleaseURL  string    `json:"release_url,omitempty"`
	Body        string    `json:"body,omitempty"` // raw release notes / changelog
}

// VersionDiff represents the delta between two versions.
type VersionDiff struct {
	ProjectID string `json:"project_id"`
	From      string `json:"from"`
	To        string `json:"to"`

	// Raw material fetched from the provider (release notes, commit messages, etc.)
	Changelog string `json:"changelog,omitempty"`

	// Filled in by the AI adapter
	Summary   string `json:"summary,omitempty"`
	Important bool   `json:"important,omitempty"`

	AnalyzedAt time.Time `json:"analyzed_at,omitempty"`
}
