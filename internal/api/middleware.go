package api

import (
	"log/slog"
	"net/http"
)

// AdminOnly protects write/config endpoints.
// It checks for a Bearer token or an X-Admin-Key header.
// When adminKey is empty, auth is disabled (useful for development).
func AdminOnly(adminKey string, log *slog.Logger) func(http.Handler) http.Handler {
	if adminKey == "" {
		log.Warn("api: AdminOnly — adminKey is empty, auth is DISABLED for all write endpoints")
		return func(next http.Handler) http.Handler { return next }
	}
	log.Debug("api: AdminOnly middleware active")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-Admin-Key")
			if key == "" {
				auth := r.Header.Get("Authorization")
				const prefix = "Bearer "
				if len(auth) > len(prefix) {
					key = auth[len(prefix):]
				}
			}
			if key != adminKey {
				log.Warn("api: unauthorized request",
					"method", r.Method,
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
					"key_provided", key != "",
				)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			log.Debug("api: request authorized", "method", r.Method, "path", r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}
