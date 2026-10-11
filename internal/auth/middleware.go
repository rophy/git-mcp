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
				unauthorized(w, `{"error":"missing authorization header"}`, "")
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token == authHeader {
				unauthorized(w, `{"error":"invalid authorization format"}`, "")
				return
			}

			identity, err := authenticator.Authenticate(token)
			if err != nil {
				logger.Warn("authentication failed", "err", err, "remote", r.RemoteAddr)
				unauthorized(w, `{"error":"authentication failed"}`, "invalid_token")
				return
			}

			ctx := audit.ContextWithService(r.Context(), identity.Service)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// unauthorized writes a 401 with a WWW-Authenticate challenge (RFC 6750).
func unauthorized(w http.ResponseWriter, body, errCode string) {
	challenge := `Bearer realm="git-mcp"`
	if errCode != "" {
		challenge += `, error="` + errCode + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	http.Error(w, body, http.StatusUnauthorized)
}
