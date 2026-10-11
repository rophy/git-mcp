package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/rophy/git-mcp/internal/auth"
	"github.com/rophy/git-mcp/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupJWKSServer(t *testing.T, key *rsa.PrivateKey, kid string) *httptest.Server {
	t.Helper()
	pubKey, err := jwk.FromRaw(key.Public())
	require.NoError(t, err)
	require.NoError(t, pubKey.Set(jwk.KeyIDKey, kid))
	require.NoError(t, pubKey.Set(jwk.AlgorithmKey, "RS256"))
	require.NoError(t, pubKey.Set(jwk.KeyUsageKey, "sig"))

	set := jwk.NewSet()
	require.NoError(t, set.AddKey(pubKey))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func newCfg(jwksURL string, services map[string]config.ServiceConfig) *config.Config {
	return &config.Config{
		Auth: config.AuthConfig{
			Audience: "git-mcp",
			Issuers: []config.IssuerConfig{
				{Name: "test-issuer", Iss: "https://auth.example.com", JWKSURI: jwksURL},
			},
		},
		Services: services,
	}
}

func TestAuthenticate_ValidToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	kid := "test-key-1"
	jwksServer := setupJWKSServer(t, key, kid)

	cfg := newCfg(jwksServer.URL, map[string]config.ServiceConfig{
		"diagnostics-agent": {
			Issuer:  "test-issuer",
			Subject: "spiffe://example.com/diagnostics",
			Repos:   []string{"github.com/org/checkout-service", "github.com/org/payment-service"},
		},
	})

	authenticator, err := auth.NewAuthenticator(cfg)
	require.NoError(t, err)

	token := signToken(t, key, kid, jwt.MapClaims{
		"iss": "https://auth.example.com",
		"sub": "spiffe://example.com/diagnostics",
		"aud": "git-mcp",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"nbf": time.Now().Add(-1 * time.Minute).Unix(),
	})

	identity, err := authenticator.Authenticate(token)
	require.NoError(t, err)
	assert.Equal(t, "diagnostics-agent", identity.Service)
	assert.Equal(t, "spiffe://example.com/diagnostics", identity.Subject)
}

func TestAuthenticate_UnknownIssuer(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	cfg := &config.Config{
		Auth:     config.AuthConfig{Audience: "git-mcp", Issuers: []config.IssuerConfig{}},
		Services: map[string]config.ServiceConfig{},
	}
	authenticator, err := auth.NewAuthenticator(cfg)
	require.NoError(t, err)

	token := signToken(t, key, "key-1", jwt.MapClaims{
		"iss": "https://unknown.example.com",
		"sub": "some-subject",
		"aud": "git-mcp",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	})

	_, err = authenticator.Authenticate(token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown issuer")
}

func TestAuthenticate_WrongAudience(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	kid := "test-key-1"
	jwksServer := setupJWKSServer(t, key, kid)

	authenticator, err := auth.NewAuthenticator(newCfg(jwksServer.URL, map[string]config.ServiceConfig{}))
	require.NoError(t, err)

	token := signToken(t, key, kid, jwt.MapClaims{
		"iss": "https://auth.example.com",
		"sub": "some-subject",
		"aud": "wrong-audience",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	})

	_, err = authenticator.Authenticate(token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audience")
}

func TestAuthenticate_ExpiredToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	kid := "test-key-1"
	jwksServer := setupJWKSServer(t, key, kid)

	authenticator, err := auth.NewAuthenticator(newCfg(jwksServer.URL, map[string]config.ServiceConfig{}))
	require.NoError(t, err)

	token := signToken(t, key, kid, jwt.MapClaims{
		"iss": "https://auth.example.com",
		"sub": "some-subject",
		"aud": "git-mcp",
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
	})

	_, err = authenticator.Authenticate(token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestAuthenticate_NoMatchingService(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	kid := "test-key-1"
	jwksServer := setupJWKSServer(t, key, kid)

	cfg := newCfg(jwksServer.URL, map[string]config.ServiceConfig{
		"other-service": {
			Issuer:  "test-issuer",
			Subject: "different-subject",
			Repos:   []string{"github.com/org/*"},
		},
	})
	authenticator, err := auth.NewAuthenticator(cfg)
	require.NoError(t, err)

	token := signToken(t, key, kid, jwt.MapClaims{
		"iss": "https://auth.example.com",
		"sub": "unknown-subject",
		"aud": "git-mcp",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	})

	_, err = authenticator.Authenticate(token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no service")
}

func TestCheckRepoAccess(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwksServer := setupJWKSServer(t, key, "test-key-1")

	cfg := newCfg(jwksServer.URL, map[string]config.ServiceConfig{
		"sre-bot": {
			Issuer:  "test-issuer",
			Subject: "sre-bot-client-id",
			Repos:   []string{"github.com/org/*"},
		},
	})
	authenticator, err := auth.NewAuthenticator(cfg)
	require.NoError(t, err)

	assert.True(t, authenticator.CheckRepoAccess("sre-bot", "github.com/org/checkout-service"))
	assert.True(t, authenticator.CheckRepoAccess("sre-bot", "github.com/org/payment-service"))
	assert.False(t, authenticator.CheckRepoAccess("sre-bot", "github.com/other-org/repo"))
	assert.False(t, authenticator.CheckRepoAccess("unknown-service", "github.com/org/repo"))
}
