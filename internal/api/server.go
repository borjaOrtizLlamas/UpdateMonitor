package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/bortizllamas/updatemonitor/internal/config"
	"github.com/bortizllamas/updatemonitor/internal/service"
)

// Server is the HTTP server wiring all routes together.
type Server struct {
	svc    *service.ProjectService
	cfg    *config.Config
	router chi.Router
}

// New creates an HTTP server and registers all routes.
func New(svc *service.ProjectService, cfg *config.Config) *Server {
	s := &Server{svc: svc, cfg: cfg}
	s.router = s.buildRouter()
	return s
}

// Handler returns the root http.Handler for use with http.ListenAndServe.
func (s *Server) Handler() http.Handler { return s.router }

// Addr returns the listen address, e.g. ":8080".
func (s *Server) Addr() string { return fmt.Sprintf(":%d", s.cfg.Server.Port) }

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Admin-Key"},
		AllowCredentials: false,
	}))

	// Health probe — always public
	r.Get("/healthz", healthz)

	// Static frontend assets
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// SPA / dashboard
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/templates/index.html")
	})

	// Public read API
	r.Route("/api", func(r chi.Router) {
		r.Get("/projects", s.listProjects)
		r.Get("/projects/{id}", s.getProject)

		// Admin-protected write API
		r.Group(func(r chi.Router) {
			r.Use(AdminOnly(s.cfg.Server.AdminKey))
			r.Post("/projects", s.addProject)
			r.Delete("/projects/{id}", s.deleteProject)
			r.Post("/projects/{id}/confirm-update", s.confirmUpdate)
			r.Post("/projects/{id}/snooze", s.snoozeProject)
			r.Delete("/projects/{id}/snooze", s.unsnoozeProject)
			r.Post("/check", s.triggerCheck)
		})
	})

	return r
}
