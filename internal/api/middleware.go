package api

import (
	"context"
	"net/http"
	"strings"
)

type ctxUserKey struct{}

// AuthMiddleware protects /api/* except health and auth bootstrap endpoints.
// Webhooks (/hooks/*) and static UI are not covered by this middleware.
func (s *Server) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		switch {
		case path == "/api/health",
			path == "/api/auth/status",
			path == "/api/auth/login",
			path == "/api/auth/setup":
			next.ServeHTTP(w, r)
			return
		}
		if s.Auth == nil {
			writeErr(w, 500, "auth not configured")
			return
		}
		claims, err := s.Auth.ParseToken(r.Header.Get("Authorization"))
		if err != nil {
			writeErr(w, 401, "未登录或登录已过期")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
