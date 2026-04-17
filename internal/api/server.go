package api

import (
	"fmt"
	"log/slog"
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
	log    *slog.Logger
	router chi.Router
}

// New creates an HTTP server and registers all routes.
func New(svc *service.ProjectService, cfg *config.Config, log *slog.Logger) *Server {
	s := &Server{svc: svc, cfg: cfg, log: log}
	s.router = s.buildRouter()
	log.Debug("api: router built",
		"admin_key_set", cfg.Server.AdminKey != "",
		"port", cfg.Server.Port,
	)
	return s
}

// Handler returns the root http.Handler for use with http.ListenAndServe.
func (s *Server) Handler() http.Handler { return s.router }

// Addr returns the listen address, e.g. ":8080".
func (s *Server) Addr() string { return fmt.Sprintf(":%d", s.cfg.Server.Port) }

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()

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

	r.Get("/healthz", healthz)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/templates/index.html")
	})

	r.Route("/api", func(r chi.Router) {
		r.Get("/projects", s.listProjects)
		r.Get("/projects/{id}", s.getProject)

		r.Group(func(r chi.Router) {
			r.Use(AdminOnly(s.cfg.Server.AdminKey, s.log))
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
