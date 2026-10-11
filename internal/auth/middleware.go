package auth

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/rophy/git-mcp/internal/audit"
)

func HTTPMiddleware(authenticator *Authenticator, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token == authHeader {
				http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
				return
			}

			identity, err := authenticator.Authenticate(token)
			if err != nil {
				logger.Warn("authentication failed", "err", err, "remote", r.RemoteAddr)
				http.Error(w, `{"error":"authentication failed"}`, http.StatusUnauthorized)
				return
			}

			ctx := audit.ContextWithService(r.Context(), identity.Service)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
