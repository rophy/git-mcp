package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rophy/git-mcp/internal/audit"
	"github.com/rophy/git-mcp/internal/auth"
	"github.com/rophy/git-mcp/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPMiddleware(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	kid := "test-key-1"
	jwksServer := setupJWKSServer(t, key, kid)

	authenticator, err := auth.NewAuthenticator(newCfg(jwksServer.URL, map[string]config.ServiceConfig{
		"diagnostics-agent": {Issuer: "test-issuer", Subject: "agent-sub", Repos: []string{"github.com/org/*"}},
	}))
	require.NoError(t, err)

	var gotService string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotService = audit.ServiceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := auth.HTTPMiddleware(authenticator, logger)(next)

	validToken := signToken(t, key, kid, jwt.MapClaims{
		"iss": "https://auth.example.com",
		"sub": "agent-sub",
		"aud": "git-mcp",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	tests := []struct {
		name        string
		path        string
		header      string
		wantStatus  int
		wantService string
	}{
		{"health bypass", "/health", "", http.StatusOK, ""},
		{"missing header", "/mcp", "", http.StatusUnauthorized, ""},
		{"non-bearer scheme", "/mcp", "Basic xxx", http.StatusUnauthorized, ""},
		{"invalid token", "/mcp", "Bearer invalid-token", http.StatusUnauthorized, ""},
		{"valid token", "/mcp", "Bearer " + validToken, http.StatusOK, "diagnostics-agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotService = ""
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantService, gotService)
		})
	}
}
