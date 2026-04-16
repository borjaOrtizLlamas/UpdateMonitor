// Package api contains the HTTP layer: server setup, handlers, and middleware.
package api

import (
	"net/http"
)

// AdminOnly is middleware that protects write/config endpoints.
// It checks for a Bearer token or an X-Admin-Key header.
// When adminKey is empty, auth is disabled (useful for development).
func AdminOnly(adminKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if adminKey == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-Admin-Key")
			if key == "" {
				// Also accept Bearer token format.
				auth := r.Header.Get("Authorization")
				const prefix = "Bearer "
				if len(auth) > len(prefix) {
					key = auth[len(prefix):]
				}
			}
			if key != adminKey {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
