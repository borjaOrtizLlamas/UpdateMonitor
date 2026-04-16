// Package service contains the core application logic (use-cases).
// It orchestrates storage, trackers, the AI analyzer, notifiers, and the CVE checker.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/bortizllamas/updatemonitor/internal/domain"
	"github.com/bortizllamas/updatemonitor/internal/ports"
)

// ProjectService coordinates all version-tracking operations.
type ProjectService struct {
	storage   ports.Storage
	trackers  map[domain.Platform]ports.VersionTracker
	ai        ports.AIAnalyzer  // nil if no AI key configured
	cve       ports.CVEChecker  // nil if not configured
	notifiers map[string]ports.Notifier
	log       *slog.Logger
}

// New creates a ProjectService with the provided dependencies.
func New(
	storage ports.Storage,
	trackers []ports.VersionTracker,
	ai ports.AIAnalyzer,
	cve ports.CVEChecker,
	notifiers []ports.Notifier,
	log *slog.Logger,
) *ProjectService {
	tm := make(map[domain.Platform]ports.VersionTracker, len(trackers))
	for _, t := range trackers {
		tm[t.Platform()] = t
	}

	nm := make(map[string]ports.Notifier, len(notifiers))
	for _, n := range notifiers {
		nm[n.Type()] = n
	}

	return &ProjectService{
		storage:   storage,
		trackers:  tm,
		ai:        ai,
		cve:       cve,
		notifiers: nm,
		log:       log,
	}
}

// AddProject validates, persists, and returns a new tracked project.
func (s *ProjectService) AddProject(ctx context.Context, req AddProjectRequest) (*domain.Project, error) {
	if _, ok := s.trackers[req.Platform]; !ok {
		return nil, fmt.Errorf("unsupported platform: %s", req.Platform)
	}

	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("load projects: %w", err)
	}

	now := time.Now().UTC()
	p := &domain.Project{
		ID:             uuid.NewString(),
		Name:           req.Name,
		URL:            req.URL,
		Platform:       req.Platform,
		Owner:          req.Owner,
		Repo:           req.Repo,
		CurrentVersion: req.CurrentVersion,
		Ecosystem:      req.Ecosystem,
		PackageName:    req.PackageName,
		Notifications:  req.Notifications,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// Immediate version + CVE check so the UI has data right away.
	if err := s.checkOne(ctx, p); err != nil {
		s.log.Warn("initial version check failed", "project", p.Name, "err", err)
	}

	store.Projects = append(store.Projects, p)
	store.UpdatedAt = now

	if err := s.storage.SaveProjects(ctx, store); err != nil {
		return nil, fmt.Errorf("save projects: %w", err)
	}
	return p, nil
}

// ListProjects returns all tracked projects.
func (s *ProjectService) ListProjects(ctx context.Context) ([]*domain.Project, error) {
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		return nil, err
	}
	return store.Projects, nil
}

// GetProject returns a single project by ID.
func (s *ProjectService) GetProject(ctx context.Context, id string) (*domain.Project, error) {
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range store.Projects {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("project %s not found", id)
}

// DeleteProject removes a project by ID.
func (s *ProjectService) DeleteProject(ctx context.Context, id string) error {
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		return err
	}

	filtered := store.Projects[:0]
	found := false
	for _, p := range store.Projects {
		if p.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, p)
	}
	if !found {
		return fmt.Errorf("project %s not found", id)
	}

	store.Projects = filtered
	store.UpdatedAt = time.Now().UTC()
	return s.storage.SaveProjects(ctx, store)
}

// ConfirmUpdate marks a project as updated to its latest known version.
// This promotes latest_version → current_version and clears all pending
// update state (summary, snooze, CVEs). CVEs will be re-evaluated on the
// next scheduled check against the new current version.
func (s *ProjectService) ConfirmUpdate(ctx context.Context, id string) (*domain.Project, error) {
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		return nil, err
	}

	p, err := findInStore(store, id)
	if err != nil {
		return nil, err
	}

	p.CurrentVersion = p.LatestVersion
	p.UpdateAvailable = false
	p.UpdateSummary = ""
	p.UpdateImportant = false
	p.SnoozedUntilVersion = ""
	p.SnoozedAt = time.Time{}
	p.CVEs = nil
	p.HasCVEs = false
	p.UpdatedAt = time.Now().UTC()

	store.UpdatedAt = time.Now().UTC()
	if err := s.storage.SaveProjects(ctx, store); err != nil {
		return nil, err
	}
	return p, nil
}

// SnoozeProject suppresses update alerts for the project's current latest
// version. The snooze expires automatically when a newer version is released.
func (s *ProjectService) SnoozeProject(ctx context.Context, id string) (*domain.Project, error) {
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		return nil, err
	}

	p, err := findInStore(store, id)
	if err != nil {
		return nil, err
	}

	if p.LatestVersion == "" {
		return nil, fmt.Errorf("no latest version known for %s — run a check first", p.Name)
	}

	p.SnoozedUntilVersion = p.LatestVersion
	p.SnoozedAt = time.Now().UTC()
	p.UpdateAvailable = false
	p.UpdatedAt = time.Now().UTC()

	store.UpdatedAt = time.Now().UTC()
	if err := s.storage.SaveProjects(ctx, store); err != nil {
		return nil, err
	}
	return p, nil
}

// UnsnoozeProject clears an active snooze and restores the update badge if
// the project is still behind.
func (s *ProjectService) UnsnoozeProject(ctx context.Context, id string) (*domain.Project, error) {
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		return nil, err
	}

	p, err := findInStore(store, id)
	if err != nil {
		return nil, err
	}

	p.SnoozedUntilVersion = ""
	p.SnoozedAt = time.Time{}
	p.UpdateAvailable = p.LatestVersion != "" && p.LatestVersion != p.CurrentVersion
	p.UpdatedAt = time.Now().UTC()

	store.UpdatedAt = time.Now().UTC()
	if err := s.storage.SaveProjects(ctx, store); err != nil {
		return nil, err
	}
	return p, nil
}

// CheckAll runs a version + CVE check for every tracked project.
// Called by the scheduler every 30 minutes.
func (s *ProjectService) CheckAll(ctx context.Context) {
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		s.log.Error("check all: load projects", "err", err)
		return
	}

	for _, p := range store.Projects {
		if err := s.checkOne(ctx, p); err != nil {
			s.log.Warn("check failed", "project", p.Name, "err", err)
		}
	}

	store.UpdatedAt = time.Now().UTC()
	if err := s.storage.SaveProjects(ctx, store); err != nil {
		s.log.Error("check all: save projects", "err", err)
	}
}

// checkOne performs a version + CVE check for a single project, updating it in place.
func (s *ProjectService) checkOne(ctx context.Context, p *domain.Project) error {
	tracker, ok := s.trackers[p.Platform]
	if !ok {
		return fmt.Errorf("no tracker for platform %s", p.Platform)
	}

	latest, err := tracker.LatestVersion(ctx, p.Owner, p.Repo)
	if err != nil {
		return fmt.Errorf("latest version: %w", err)
	}

	p.LastChecked = time.Now().UTC()

	// ── Snooze expiry ────────────────────────────────────────────────────────
	// If a new version has been released past the snoozed one, clear the snooze
	// automatically so the user is notified about the newer release.
	if p.SnoozedUntilVersion != "" && latest.Tag != p.SnoozedUntilVersion {
		s.log.Info("snooze expired — newer version available",
			"project", p.Name,
			"snoozed", p.SnoozedUntilVersion,
			"new", latest.Tag,
		)
		p.SnoozedUntilVersion = ""
		p.SnoozedAt = time.Time{}
	}

	isSnoozed := p.SnoozedUntilVersion != "" && p.SnoozedUntilVersion == latest.Tag

	previousLatest := p.LatestVersion
	p.LatestVersion = latest.Tag
	p.UpdateAvailable = (p.LatestVersion != p.CurrentVersion) && !isSnoozed
	p.UpdatedAt = time.Now().UTC()

	// ── Changelog + AI analysis ───────────────────────────────────────────────
	// Only re-analyze when the latest version actually changed AND an update
	// is visible to the user (i.e. not snoozed).
	if p.UpdateAvailable && previousLatest != latest.Tag && p.CurrentVersion != "" {
		// Always diff from currentVersion so the summary covers everything the
		// user has not yet applied — not just the last incremental step.
		changelog, err := tracker.Changelog(ctx, p.Owner, p.Repo, p.CurrentVersion, latest.Tag)
		if err != nil {
			s.log.Warn("changelog fetch failed", "project", p.Name, "err", err)
		}

		diff := &domain.VersionDiff{
			ProjectID: p.ID,
			From:      p.CurrentVersion,
			To:        latest.Tag,
			Changelog: changelog,
		}

		if s.ai != nil {
			enriched, err := s.ai.Analyze(ctx, diff)
			if err != nil {
				s.log.Warn("ai analyze failed", "project", p.Name, "err", err)
			} else {
				diff = enriched
			}
		}

		p.UpdateSummary = diff.Summary
		p.UpdateImportant = diff.Important

		s.notify(ctx, p)
	}

	// ── CVE check ─────────────────────────────────────────────────────────────
	// Always re-check CVEs against the version the user is currently running.
	if s.cve != nil && p.Ecosystem != "" && p.PackageName != "" && p.CurrentVersion != "" {
		cves, err := s.cve.Check(ctx, p.Ecosystem, p.PackageName, p.CurrentVersion)
		if err != nil {
			s.log.Warn("cve check failed", "project", p.Name, "err", err)
		} else {
			p.CVEs = cves
			p.HasCVEs = len(cves) > 0
		}
	}

	return nil
}

// notify dispatches notifications for all configured targets on the project.
func (s *ProjectService) notify(ctx context.Context, p *domain.Project) {
	seen := map[string]bool{}
	for _, target := range p.Notifications {
		if seen[target.Type] {
			continue
		}
		seen[target.Type] = true

		n, ok := s.notifiers[target.Type]
		if !ok {
			s.log.Warn("unknown notifier type", "type", target.Type)
			continue
		}
		if err := n.Notify(ctx, p); err != nil {
			s.log.Warn("notification failed", "type", target.Type, "project", p.Name, "err", err)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func findInStore(store *domain.ProjectStore, id string) (*domain.Project, error) {
	for _, p := range store.Projects {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("project %s not found", id)
}

// AddProjectRequest is the input DTO for AddProject.
type AddProjectRequest struct {
	Name           string
	URL            string
	Platform       domain.Platform
	Owner          string
	Repo           string
	CurrentVersion string
	Ecosystem      string
	PackageName    string
	Notifications  []domain.NotificationTarget
}
