// Package service contains the core application logic (use-cases).
package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bortizllamas/updatemonitor/internal/domain"
	"github.com/bortizllamas/updatemonitor/internal/ports"
)

// cveIDRegex matches standard CVE identifiers and GitHub Security Advisory IDs.
var cveIDRegex = regexp.MustCompile(`(?i)(CVE-\d{4}-\d{4,}|GHSA-[a-zA-Z0-9]{4}-[a-zA-Z0-9]{4}-[a-zA-Z0-9]{4})`)

// ProjectService coordinates all version-tracking operations.
type ProjectService struct {
	storage   ports.Storage
	trackers  map[domain.Platform]ports.VersionTracker
	ai        ports.AIAnalyzer
	cve       ports.CVEChecker
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
		log.Debug("service: tracker registered", "platform", t.Platform())
	}

	nm := make(map[string]ports.Notifier, len(notifiers))
	for _, n := range notifiers {
		nm[n.Type()] = n
		log.Debug("service: notifier registered", "type", n.Type())
	}

	log.Debug("service: ProjectService created",
		"trackers", len(tm),
		"notifiers", len(nm),
		"ai_enabled", ai != nil,
		"cve_enabled", cve != nil,
	)

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
	s.log.Debug("service: AddProject called",
		"name", req.Name,
		"url", req.URL,
		"platform", req.Platform,
		"owner", req.Owner,
		"repo", req.Repo,
		"current_version", req.CurrentVersion,
		"ecosystem", req.Ecosystem,
		"package_name", req.PackageName,
	)

	if _, ok := s.trackers[req.Platform]; !ok {
		s.log.Error("service: AddProject — no tracker for platform", "platform", req.Platform)
		return nil, fmt.Errorf("unsupported platform: %s", req.Platform)
	}

	s.log.Debug("service: AddProject — loading existing projects from storage")
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		s.log.Error("service: AddProject — failed to load projects", "err", err)
		return nil, fmt.Errorf("load projects: %w", err)
	}
	s.log.Debug("service: AddProject — existing projects loaded", "count", len(store.Projects))

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
	s.log.Debug("service: AddProject — project object created", "id", p.ID, "name", p.Name)

	s.log.Debug("service: AddProject — running initial version+CVE check", "project", p.Name)
	if err := s.checkOne(ctx, p); err != nil {
		s.log.Warn("service: AddProject — initial version check failed (non-fatal)",
			"project", p.Name, "err", err)
	}

	store.Projects = append(store.Projects, p)
	store.UpdatedAt = now
	s.log.Debug("service: AddProject — saving projects to storage", "total_projects", len(store.Projects))

	if err := s.storage.SaveProjects(ctx, store); err != nil {
		s.log.Error("service: AddProject — SaveProjects failed", "project", p.Name, "err", err)
		return nil, fmt.Errorf("save projects: %w", err)
	}
	s.log.Info("service: project added and saved",
		"id", p.ID,
		"name", p.Name,
		"platform", p.Platform,
		"current_version", p.CurrentVersion,
		"latest_version", p.LatestVersion,
		"update_available", p.UpdateAvailable,
		"has_cves", p.HasCVEs,
	)
	return p, nil
}

// ListProjects returns all tracked projects.
func (s *ProjectService) ListProjects(ctx context.Context) ([]*domain.Project, error) {
	s.log.Debug("service: ListProjects called")
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		s.log.Error("service: ListProjects — storage load failed", "err", err)
		return nil, err
	}
	s.log.Debug("service: ListProjects — returned", "count", len(store.Projects))
	return store.Projects, nil
}

// GetProject returns a single project by ID.
func (s *ProjectService) GetProject(ctx context.Context, id string) (*domain.Project, error) {
	s.log.Debug("service: GetProject called", "id", id)
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		s.log.Error("service: GetProject — storage load failed", "id", id, "err", err)
		return nil, err
	}
	for _, p := range store.Projects {
		if p.ID == id {
			s.log.Debug("service: GetProject — found", "id", id, "name", p.Name)
			return p, nil
		}
	}
	s.log.Warn("service: GetProject — not found", "id", id)
	return nil, fmt.Errorf("project %s not found", id)
}

// DeleteProject removes a project by ID.
func (s *ProjectService) DeleteProject(ctx context.Context, id string) error {
	s.log.Debug("service: DeleteProject called", "id", id)
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		s.log.Error("service: DeleteProject — storage load failed", "id", id, "err", err)
		return err
	}

	filtered := store.Projects[:0]
	found := false
	for _, p := range store.Projects {
		if p.ID == id {
			found = true
			s.log.Debug("service: DeleteProject — found project to delete", "id", id, "name", p.Name)
			continue
		}
		filtered = append(filtered, p)
	}
	if !found {
		s.log.Warn("service: DeleteProject — project not found", "id", id)
		return fmt.Errorf("project %s not found", id)
	}

	store.Projects = filtered
	store.UpdatedAt = time.Now().UTC()
	s.log.Debug("service: DeleteProject — saving after delete", "remaining", len(store.Projects))
	if err := s.storage.SaveProjects(ctx, store); err != nil {
		s.log.Error("service: DeleteProject — SaveProjects failed", "id", id, "err", err)
		return err
	}
	s.log.Info("service: project deleted", "id", id)
	return nil
}

// ConfirmUpdate marks a project as updated to its latest known version.
func (s *ProjectService) ConfirmUpdate(ctx context.Context, id string) (*domain.Project, error) {
	s.log.Debug("service: ConfirmUpdate called", "id", id)
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		s.log.Error("service: ConfirmUpdate — storage load failed", "id", id, "err", err)
		return nil, err
	}

	p, err := findInStore(store, id)
	if err != nil {
		s.log.Warn("service: ConfirmUpdate — project not found", "id", id)
		return nil, err
	}

	s.log.Debug("service: ConfirmUpdate — promoting version",
		"id", id, "from", p.CurrentVersion, "to", p.LatestVersion)
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
		s.log.Error("service: ConfirmUpdate — SaveProjects failed", "id", id, "err", err)
		return nil, err
	}
	s.log.Info("service: update confirmed", "id", id, "current_version", p.CurrentVersion)
	return p, nil
}

// SnoozeProject suppresses update alerts for the project's current latest version.
func (s *ProjectService) SnoozeProject(ctx context.Context, id string) (*domain.Project, error) {
	s.log.Debug("service: SnoozeProject called", "id", id)
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		s.log.Error("service: SnoozeProject — storage load failed", "id", id, "err", err)
		return nil, err
	}

	p, err := findInStore(store, id)
	if err != nil {
		s.log.Warn("service: SnoozeProject — project not found", "id", id)
		return nil, err
	}

	if p.LatestVersion == "" {
		s.log.Warn("service: SnoozeProject — no latest version known", "id", id)
		return nil, fmt.Errorf("no latest version known for %s — run a check first", p.Name)
	}

	p.SnoozedUntilVersion = p.LatestVersion
	p.SnoozedAt = time.Now().UTC()
	p.UpdateAvailable = false
	p.UpdatedAt = time.Now().UTC()

	store.UpdatedAt = time.Now().UTC()
	if err := s.storage.SaveProjects(ctx, store); err != nil {
		s.log.Error("service: SnoozeProject — SaveProjects failed", "id", id, "err", err)
		return nil, err
	}
	s.log.Info("service: project snoozed", "id", id, "until_version", p.SnoozedUntilVersion)
	return p, nil
}

// UnsnoozeProject clears an active snooze.
func (s *ProjectService) UnsnoozeProject(ctx context.Context, id string) (*domain.Project, error) {
	s.log.Debug("service: UnsnoozeProject called", "id", id)
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		s.log.Error("service: UnsnoozeProject — storage load failed", "id", id, "err", err)
		return nil, err
	}

	p, err := findInStore(store, id)
	if err != nil {
		s.log.Warn("service: UnsnoozeProject — project not found", "id", id)
		return nil, err
	}

	p.SnoozedUntilVersion = ""
	p.SnoozedAt = time.Time{}
	p.UpdateAvailable = p.LatestVersion != "" && p.LatestVersion != p.CurrentVersion
	p.UpdatedAt = time.Now().UTC()

	store.UpdatedAt = time.Now().UTC()
	if err := s.storage.SaveProjects(ctx, store); err != nil {
		s.log.Error("service: UnsnoozeProject — SaveProjects failed", "id", id, "err", err)
		return nil, err
	}
	s.log.Info("service: project unsnoozed", "id", id, "update_available", p.UpdateAvailable)
	return p, nil
}

// CheckAll runs a version + CVE check for every tracked project.
func (s *ProjectService) CheckAll(ctx context.Context) {
	s.log.Debug("service: CheckAll started")
	store, err := s.storage.LoadProjects(ctx)
	if err != nil {
		s.log.Error("service: CheckAll — failed to load projects", "err", err)
		return
	}
	s.log.Debug("service: CheckAll — projects loaded", "count", len(store.Projects))

	for _, p := range store.Projects {
		s.log.Debug("service: CheckAll — checking project", "name", p.Name, "id", p.ID)
		if err := s.checkOne(ctx, p); err != nil {
			s.log.Warn("service: CheckAll — check failed", "project", p.Name, "err", err)
		}
	}

	store.UpdatedAt = time.Now().UTC()
	s.log.Debug("service: CheckAll — saving updated projects")
	if err := s.storage.SaveProjects(ctx, store); err != nil {
		s.log.Error("service: CheckAll — SaveProjects failed", "err", err)
	} else {
		s.log.Debug("service: CheckAll complete", "projects_checked", len(store.Projects))
	}
}

// checkOne performs a version + CVE check for a single project.
func (s *ProjectService) checkOne(ctx context.Context, p *domain.Project) error {
	s.log.Debug("service: checkOne started",
		"project", p.Name,
		"platform", p.Platform,
		"current_version", p.CurrentVersion,
	)

	tracker, ok := s.trackers[p.Platform]
	if !ok {
		s.log.Error("service: checkOne — no tracker for platform",
			"project", p.Name, "platform", p.Platform)
		return fmt.Errorf("no tracker for platform %s", p.Platform)
	}

	s.log.Debug("service: checkOne — fetching latest version",
		"project", p.Name, "owner", p.Owner, "repo", p.Repo)
	latest, err := tracker.LatestVersion(ctx, p.Owner, p.Repo)
	if err != nil {
		s.log.Error("service: checkOne — LatestVersion failed",
			"project", p.Name, "err", err)
		return fmt.Errorf("latest version: %w", err)
	}
	s.log.Debug("service: checkOne — latest version fetched",
		"project", p.Name, "tag", latest.Tag,
		"previous_latest", p.LatestVersion,
		"current", p.CurrentVersion,
	)

	p.LastChecked = time.Now().UTC()

	// ── Snooze expiry ────────────────────────────────────────────────────────
	if p.SnoozedUntilVersion != "" && latest.Tag != p.SnoozedUntilVersion {
		s.log.Info("service: snooze expired — newer version available",
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

	s.log.Debug("service: checkOne — version state",
		"project", p.Name,
		"current", p.CurrentVersion,
		"latest", p.LatestVersion,
		"update_available", p.UpdateAvailable,
		"is_snoozed", isSnoozed,
	)

	// ── Changelog + AI analysis ──────────────────────────────────────────────
	if p.UpdateAvailable && previousLatest != latest.Tag && p.CurrentVersion != "" {
		s.log.Debug("service: checkOne — new version detected, fetching changelog",
			"project", p.Name, "from", p.CurrentVersion, "to", latest.Tag)

		changelog, err := tracker.Changelog(ctx, p.Owner, p.Repo, p.CurrentVersion, latest.Tag)
		if err != nil {
			s.log.Warn("service: checkOne — changelog fetch failed", "project", p.Name, "err", err)
		} else {
			s.log.Debug("service: checkOne — changelog fetched", "project", p.Name, "bytes", len(changelog))
		}

		diff := &domain.VersionDiff{
			ProjectID: p.ID,
			From:      p.CurrentVersion,
			To:        latest.Tag,
			Changelog: changelog,
		}

		if s.ai != nil {
			s.log.Debug("service: checkOne — running AI analysis", "project", p.Name)
			enriched, err := s.ai.Analyze(ctx, diff)
			if err != nil {
				s.log.Warn("service: checkOne — AI analysis failed", "project", p.Name, "err", err)
			} else {
				diff = enriched
				s.log.Debug("service: checkOne — AI analysis done",
					"project", p.Name,
					"important", diff.Important,
					"summary_len", len(diff.Summary),
				)
			}
		} else {
			s.log.Debug("service: checkOne — AI disabled, skipping analysis")
		}

		p.UpdateSummary = diff.Summary
		p.UpdateImportant = diff.Important
		s.notify(ctx, p)
	} else {
		s.log.Debug("service: checkOne — skipping changelog/AI",
			"project", p.Name,
			"update_available", p.UpdateAvailable,
			"previous_latest", previousLatest,
			"latest", latest.Tag,
			"current_version_set", p.CurrentVersion != "",
		)
	}

	// ── CVE check ─────────────────────────────────────────────────────────────
	if s.cve == nil {
		s.log.Debug("service: checkOne — CVE checker is nil, skipping", "project", p.Name)
	} else {
		s.log.Debug("service: checkOne — starting CVE check", "project", p.Name)
		var cves []domain.CVE

		// 1. OSV package-based check
		if p.Ecosystem != "" && p.PackageName != "" && p.CurrentVersion != "" {
			s.log.Debug("service: checkOne — running OSV package check",
				"project", p.Name,
				"ecosystem", p.Ecosystem,
				"package", p.PackageName,
				"version", p.CurrentVersion,
			)
			if found, err := s.cve.Check(ctx, p.Ecosystem, p.PackageName, p.CurrentVersion); err != nil {
				s.log.Warn("service: checkOne — OSV package check failed",
					"project", p.Name, "err", err)
			} else {
				s.log.Debug("service: checkOne — OSV package check done",
					"project", p.Name, "cves_found", len(found))
				cves = append(cves, found...)
			}
		} else {
			s.log.Debug("service: checkOne — skipping OSV package check (ecosystem/package not set)",
				"project", p.Name,
				"ecosystem", p.Ecosystem,
				"package", p.PackageName,
			)
		}

		// 2. Scan release notes + commit messages for CVE IDs
		if p.CurrentVersion != "" && p.LatestVersion != "" && p.CurrentVersion != p.LatestVersion {
			s.log.Debug("service: checkOne — scanning releases/commits for CVE IDs",
				"project", p.Name, "from", p.CurrentVersion, "to", p.LatestVersion)

			changes, err := tracker.AllChanges(ctx, p.Owner, p.Repo, p.CurrentVersion, p.LatestVersion)
			if err != nil {
				s.log.Warn("service: checkOne — AllChanges failed", "project", p.Name, "err", err)
			} else {
				ids := extractCVEIDs(changes)
				s.log.Debug("service: checkOne — CVE IDs found in release text",
					"project", p.Name, "ids", ids)

				for _, id := range ids {
					if cveAlreadyPresent(cves, id) {
						s.log.Debug("service: checkOne — CVE already in list, skipping",
							"project", p.Name, "id", id)
						continue
					}
					s.log.Debug("service: checkOne — looking up CVE in OSV",
						"project", p.Name, "id", id)
					cve, err := s.cve.LookupByID(ctx, id)
					if err != nil {
						s.log.Warn("service: checkOne — CVE lookup failed",
							"project", p.Name, "id", id, "err", err)
						cves = append(cves, domain.CVE{ID: id, Severity: "UNKNOWN"})
						continue
					}
					if cve != nil {
						s.log.Debug("service: checkOne — CVE enriched from OSV",
							"id", id, "severity", cve.Severity)
						cves = append(cves, *cve)
					} else {
						s.log.Debug("service: checkOne — CVE not in OSV, recording as UNKNOWN",
							"id", id)
						cves = append(cves, domain.CVE{ID: id, Severity: "UNKNOWN"})
					}
				}
			}
		} else {
			s.log.Debug("service: checkOne — skipping release CVE scan (versions equal or missing)",
				"project", p.Name,
				"current", p.CurrentVersion,
				"latest", p.LatestVersion,
			)
		}

		p.CVEs = cves
		p.HasCVEs = len(cves) > 0
		s.log.Debug("service: checkOne — CVE check complete",
			"project", p.Name, "total_cves", len(cves), "has_cves", p.HasCVEs)
	}

	s.log.Debug("service: checkOne finished", "project", p.Name)
	return nil
}

// notify dispatches notifications for all configured targets on the project.
func (s *ProjectService) notify(ctx context.Context, p *domain.Project) {
	s.log.Debug("service: notify called", "project", p.Name, "targets", len(p.Notifications))
	seen := map[string]bool{}
	for _, target := range p.Notifications {
		if seen[target.Type] {
			continue
		}
		seen[target.Type] = true

		n, ok := s.notifiers[target.Type]
		if !ok {
			s.log.Warn("service: notify — unknown notifier type", "type", target.Type)
			continue
		}
		s.log.Debug("service: notify — sending notification", "type", target.Type, "project", p.Name)
		if err := n.Notify(ctx, p); err != nil {
			s.log.Warn("service: notify — notification failed",
				"type", target.Type, "project", p.Name, "err", err)
		} else {
			s.log.Debug("service: notify — notification sent", "type", target.Type, "project", p.Name)
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

func extractCVEIDs(releases []domain.ReleaseInfo) []string {
	seen := map[string]bool{}
	var ids []string
	for _, r := range releases {
		for _, match := range cveIDRegex.FindAllString(r.Body, -1) {
			id := strings.ToUpper(match)
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		for _, msg := range r.Commits {
			for _, match := range cveIDRegex.FindAllString(msg, -1) {
				id := strings.ToUpper(match)
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

func cveAlreadyPresent(cves []domain.CVE, id string) bool {
	for _, c := range cves {
		if strings.EqualFold(c.ID, id) {
			return true
		}
	}
	return false
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
