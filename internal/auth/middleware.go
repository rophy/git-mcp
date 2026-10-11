package auth

import (
	"net/http"
	"strings"

	"github.com/rophy/git-mcp/internal/audit"
)

func HTTPMiddleware(authenticator *Authenticator) func(http.Handler) http.Handler {
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
				http.Error(w, `{"error":"authentication failed"}`, http.StatusUnauthorized)
				return
			}

			ctx := audit.ContextWithService(r.Context(), identity.Service)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
