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
	projects, err := s.svc.ListProjects(r.Context())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, projects)
}

// getProject handles GET /api/projects/{id}
func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.svc.GetProject(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
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

// addProject handles POST /api/projects (admin-protected)
func (s *Server) addProject(w http.ResponseWriter, r *http.Request) {
	var req addProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	platform, owner, repo, err := parseRepoURL(req.URL)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		req.Name = owner + "/" + repo
	}

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
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonOK(w, p)
}

// deleteProject handles DELETE /api/projects/{id} (admin-protected)
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.DeleteProject(r.Context(), id); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// confirmUpdate handles POST /api/projects/{id}/confirm-update (admin-protected)
// Promotes latest_version → current_version and clears all pending update state.
func (s *Server) confirmUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.svc.ConfirmUpdate(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonOK(w, p)
}

// snoozeProject handles POST /api/projects/{id}/snooze (admin-protected)
// Suppresses update alerts for the project's current latest version.
func (s *Server) snoozeProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.svc.SnoozeProject(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonOK(w, p)
}

// unsnoozeProject handles DELETE /api/projects/{id}/snooze (admin-protected)
// Clears the snooze and restores the update badge if still applicable.
func (s *Server) unsnoozeProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.svc.UnsnoozeProject(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonOK(w, p)
}

// triggerCheck handles POST /api/check (admin-protected)
func (s *Server) triggerCheck(w http.ResponseWriter, r *http.Request) {
	go s.svc.CheckAll(r.Context())
	jsonOK(w, map[string]string{"status": "check triggered"})
}

// healthz handles GET /healthz — plain liveness probe.
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// ── helpers ───────────────────────────────────────────────────────────────────

// parseRepoURL extracts platform, owner, and repo from a GitHub/GitLab URL.
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
