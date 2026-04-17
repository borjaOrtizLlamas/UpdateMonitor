package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bortizllamas/updatemonitor/internal/domain"
	"github.com/bortizllamas/updatemonitor/internal/service"
)

// listProjects handles GET /api/projects
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	s.log.Debug("api: listProjects called")
	projects, err := s.svc.ListProjects(r.Context())
	if err != nil {
		s.log.Error("api: listProjects failed", "err", err)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.log.Debug("api: listProjects success", "count", len(projects))
	jsonOK(w, projects)
}

// getProject handles GET /api/projects/{id}
func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.log.Debug("api: getProject called", "id", id)
	p, err := s.svc.GetProject(r.Context(), id)
	if err != nil {
		s.log.Warn("api: getProject not found", "id", id, "err", err)
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	s.log.Debug("api: getProject success", "id", id, "name", p.Name)
	jsonOK(w, p)
}

// addProjectRequest is the JSON body for POST /api/projects
type addProjectRequest struct {
	Name           string                      `json:"name"`
	URL            string                      `json:"url"`
	CurrentVersion string                      `json:"current_version"`
	Ecosystem      string                      `json:"ecosystem,omitempty"`
	PackageName    string                      `json:"package_name,omitempty"`
	Notifications  []domain.NotificationTarget `json:"notifications,omitempty"`
}

// addProject handles POST /api/projects
func (s *Server) addProject(w http.ResponseWriter, r *http.Request) {
	s.log.Debug("api: addProject called")
	var req addProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("api: addProject — invalid request body", "err", err)
		jsonError(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.log.Debug("api: addProject request decoded",
		"url", req.URL,
		"current_version", req.CurrentVersion,
		"name", req.Name,
		"ecosystem", req.Ecosystem,
		"package_name", req.PackageName,
	)

	platform, owner, repo, err := parseRepoURL(req.URL)
	if err != nil {
		s.log.Warn("api: addProject — failed to parse URL", "url", req.URL, "err", err)
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.log.Debug("api: addProject URL parsed", "platform", platform, "owner", owner, "repo", repo)

	if req.Name == "" {
		req.Name = owner + "/" + repo
	}

	s.log.Debug("api: calling service.AddProject", "name", req.Name, "platform", platform)
	p, err := s.svc.AddProject(r.Context(), service.AddProjectRequest{
		Name:           req.Name,
		URL:            req.URL,
		Platform:       platform,
		Owner:          owner,
		Repo:           repo,
		CurrentVersion: req.CurrentVersion,
		Ecosystem:      req.Ecosystem,
		PackageName:    req.PackageName,
		Notifications:  req.Notifications,
	})
	if err != nil {
		s.log.Error("api: addProject service error", "name", req.Name, "err", err)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.log.Info("api: project added", "id", p.ID, "name", p.Name, "platform", platform)
	w.WriteHeader(http.StatusCreated)
	jsonOK(w, p)
}

// deleteProject handles DELETE /api/projects/{id}
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.log.Debug("api: deleteProject called", "id", id)
	if err := s.svc.DeleteProject(r.Context(), id); err != nil {
		s.log.Warn("api: deleteProject not found", "id", id, "err", err)
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	s.log.Info("api: project deleted", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

// confirmUpdate handles POST /api/projects/{id}/confirm-update
func (s *Server) confirmUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.log.Debug("api: confirmUpdate called", "id", id)
	p, err := s.svc.ConfirmUpdate(r.Context(), id)
	if err != nil {
		s.log.Warn("api: confirmUpdate failed", "id", id, "err", err)
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	s.log.Info("api: update confirmed", "id", id, "new_current", p.CurrentVersion)
	jsonOK(w, p)
}

// snoozeProject handles POST /api/projects/{id}/snooze
func (s *Server) snoozeProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.log.Debug("api: snoozeProject called", "id", id)
	p, err := s.svc.SnoozeProject(r.Context(), id)
	if err != nil {
		s.log.Warn("api: snoozeProject failed", "id", id, "err", err)
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	s.log.Info("api: project snoozed", "id", id, "until_version", p.SnoozedUntilVersion)
	jsonOK(w, p)
}

// unsnoozeProject handles DELETE /api/projects/{id}/snooze
func (s *Server) unsnoozeProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.log.Debug("api: unsnoozeProject called", "id", id)
	p, err := s.svc.UnsnoozeProject(r.Context(), id)
	if err != nil {
		s.log.Warn("api: unsnoozeProject failed", "id", id, "err", err)
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	s.log.Info("api: project unsnoozed", "id", id)
	jsonOK(w, p)
}

// triggerCheck handles POST /api/check
func (s *Server) triggerCheck(w http.ResponseWriter, r *http.Request) {
	s.log.Info("api: manual check triggered")
	go s.svc.CheckAll(r.Context())
	jsonOK(w, map[string]string{"status": "check triggered"})
}

// healthz handles GET /healthz
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseRepoURL(rawURL string) (domain.Platform, string, string, error) {
	rawURL = strings.TrimSuffix(rawURL, "/")
	rawURL = strings.TrimSuffix(rawURL, ".git")

	var platform domain.Platform
	var path string

	switch {
	case strings.Contains(rawURL, "github.com"):
		platform = domain.PlatformGitHub
		path = strings.SplitN(rawURL, "github.com/", 2)[1]
	case strings.Contains(rawURL, "gitlab."):
		platform = domain.PlatformGitLab
		parts := strings.SplitN(rawURL, "gitlab.", 2)
		after := parts[1]
		slashIdx := strings.Index(after, "/")
		if slashIdx == -1 {
			return "", "", "", ErrInvalidURL
		}
		path = after[slashIdx+1:]
	default:
		return "", "", "", ErrInvalidURL
	}

	segments := strings.SplitN(path, "/", 2)
	if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return "", "", "", ErrInvalidURL
	}
	return platform, segments[0], segments[1], nil
}

var ErrInvalidURL = &apiError{Message: "URL must be a public GitHub or GitLab repository URL"}

type apiError struct{ Message string }

func (e *apiError) Error() string { return e.Message }

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

