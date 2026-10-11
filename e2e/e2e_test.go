//go:build e2e

// Package e2e tests a running git-mcp container through HTTP and MCP.
// Run with `make e2e-test`; see compose.yaml for the environment.
package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rophy/git-mcp/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	issuer = "https://issuer.e2e.test"
	kid    = "e2e-key"
	repoA  = "git.test/org/repo-a"
	repoB  = "git.test/org/repo-b"
	secret = "git.test/other/secret"
)

var (
	baseURL    string
	signingKey *rsa.PrivateKey
)

func TestMain(m *testing.M) {
	baseURL = os.Getenv("GIT_MCP_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}

	var err error
	signingKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	pub, err := jwk.FromRaw(signingKey.Public())
	if err != nil {
		panic(err)
	}
	_ = pub.Set(jwk.KeyIDKey, kid)
	_ = pub.Set(jwk.AlgorithmKey, "RS256")
	set := jwk.NewSet()
	_ = set.AddKey(pub)

	// git-mcp fetches the JWKS from this server (jwks_uri in config.yaml).
	listen := os.Getenv("JWKS_LISTEN")
	if listen == "" {
		listen = ":8081"
	}
	go func() {
		http.HandleFunc("/jwks.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(set)
		})
		panic(http.ListenAndServe(listen, nil))
	}()

	os.Exit(m.Run())
}

func token(t *testing.T, subject string, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": issuer,
		"sub": subject,
		"aud": "git-mcp",
		"exp": exp.Unix(),
	})
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(signingKey)
	require.NoError(t, err)
	return signed
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// connect opens an MCP session as the given service subject.
func connect(t *testing.T, subject string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   baseURL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: token(t, subject, time.Now().Add(time.Hour))}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })
	return session
}

// call runs a tool and decodes its structured output into out.
// It returns the tool error text, or "" on success.
func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	if res.IsError {
		var msg strings.Builder
		for _, c := range res.Content {
			if text, ok := c.(*mcp.TextContent); ok {
				msg.WriteString(text.Text)
			}
		}
		return msg.String()
	}
	if out != nil {
		raw, err := json.Marshal(res.StructuredContent)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, out))
	}
	return ""
}

func TestHealth(t *testing.T) {
	resp, err := http.Get(baseURL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAuth_RejectsBadCredentials(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		challenge string
	}{
		{"missing token", "", `Bearer realm="git-mcp"`},
		{"malformed token", "Bearer not-a-jwt", `Bearer realm="git-mcp", error="invalid_token"`},
		{"expired token", "Bearer " + token(t, "reader", time.Now().Add(-time.Hour)), `Bearer realm="git-mcp", error="invalid_token"`},
		{"unknown subject", "Bearer " + token(t, "nobody", time.Now().Add(time.Hour)), `Bearer realm="git-mcp", error="invalid_token"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(`{}`))
			require.NoError(t, err)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			assert.Equal(t, tt.challenge, resp.Header.Get("WWW-Authenticate"))
		})
	}
}

func TestTools_FullWorkflow(t *testing.T) {
	session := connect(t, "reader")

	var ref tools.ResolveRefOutput
	require.Empty(t, call(t, session, "resolve_ref", map[string]any{"repo": repoA, "ref": "v1.0.0"}, &ref))
	require.Len(t, ref.SHA, 40)
	sha := ref.SHA

	var search tools.SearchCodeOutput
	require.Empty(t, call(t, session, "search_code", map[string]any{"repo": repoA, "ref": sha, "pattern": "func Handle"}, &search))
	require.Len(t, search.Matches, 1)
	assert.Equal(t, "pkg/handler.go", search.Matches[0].Path)
	assert.Equal(t, 3, search.Matches[0].LineNumber)

	var file tools.ReadFileOutput
	require.Empty(t, call(t, session, "read_file", map[string]any{"repo": repoA, "ref": sha, "path": "pkg/handler.go"}, &file))
	assert.Contains(t, file.Content, "3\tfunc Handle(err error) string {")

	var list tools.ListFilesOutput
	require.Empty(t, call(t, session, "list_files", map[string]any{"repo": repoA, "ref": sha, "recursive": true}, &list))
	assert.ElementsMatch(t, []tools.FileEntry{
		{Name: "README.md", Type: "file"},
		{Name: "pkg/handler.go", Type: "file"},
	}, list.Entries)

	var log tools.GitLogOutput
	require.Empty(t, call(t, session, "git_log", map[string]any{"repo": repoA, "ref": "main"}, &log))
	require.Len(t, log.Commits, 2)
	assert.Equal(t, "add handler", log.Commits[0].Message)
	assert.Equal(t, sha, log.Commits[0].SHA)

	var show tools.GitShowOutput
	require.Empty(t, call(t, session, "git_show", map[string]any{"repo": repoA, "ref": sha, "commit": sha}, &show))
	assert.Equal(t, "add handler", show.Message)
	assert.Contains(t, show.Diff, "+func Handle(err error) string {")
}

func TestAccess_EnforcesServiceRepos(t *testing.T) {
	reader := connect(t, "reader")
	narrow := connect(t, "narrow")
	args := func(repo string) map[string]any { return map[string]any{"repo": repo, "ref": "main"} }

	tests := []struct {
		name    string
		session *mcp.ClientSession
		repo    string
		allowed bool
	}{
		{"wildcard allows repo in org", reader, repoB, true},
		{"wildcard denies other org", reader, secret, false},
		{"exact allows listed repo", narrow, repoA, true},
		{"exact denies unlisted repo", narrow, repoB, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errText := call(t, tt.session, "resolve_ref", args(tt.repo), nil)
			if tt.allowed {
				assert.Empty(t, errText)
			} else {
				assert.Contains(t, errText, "access denied to repo "+tt.repo)
			}
		})
	}
}
