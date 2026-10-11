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
	"path/filepath"
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

	var diff tools.GitDiffOutput
	require.Empty(t, call(t, session, "git_diff", map[string]any{"repo": repoA, "base": log.Commits[1].SHA, "head": "main"}, &diff))
	assert.Equal(t, sha, diff.HeadSHA)
	assert.Contains(t, diff.Diff, "+func Handle(err error) string {")

	start, end := 3, 3
	var blame tools.GitBlameOutput
	require.Empty(t, call(t, session, "git_blame", map[string]any{"repo": repoA, "ref": sha, "path": "pkg/handler.go", "start_line": start, "end_line": end}, &blame))
	require.Len(t, blame.Lines, 1)
	assert.Equal(t, tools.BlameLine{Line: 3, Commit: sha, Text: "func Handle(err error) string {"}, blame.Lines[0])
	assert.Equal(t, "add handler", blame.Commits[sha].Summary)

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

func TestValidation_RejectsMaliciousInput(t *testing.T) {
	session := connect(t, "reader")

	tests := []struct {
		name    string
		tool    string
		args    map[string]any
		wantErr string
	}{
		{"read_file path traversal", "read_file",
			map[string]any{"repo": repoA, "ref": "main", "path": "../../../etc/passwd"}, "path contains '..'"},
		{"read_file absolute path", "read_file",
			map[string]any{"repo": repoA, "ref": "main", "path": "/etc/passwd"}, "path is absolute"},
		{"read_file .git dir", "read_file",
			map[string]any{"repo": repoA, "ref": "main", "path": ".git/config"}, "path accesses .git"},
		{"list_files traversal", "list_files",
			map[string]any{"repo": repoA, "ref": "main", "path": "pkg/../.."}, "path contains '..'"},
		{"search_code option pattern", "search_code",
			map[string]any{"repo": repoA, "ref": "main", "pattern": "--open-files-in-pager=sh"}, "pattern must not start with -"},
		{"search_code option path", "search_code",
			map[string]any{"repo": repoA, "ref": "main", "pattern": "x", "path": "--output=/tmp/x"}, "path must not start with -"},
		{"git_log option ref", "git_log",
			map[string]any{"repo": repoA, "ref": "--output=/tmp/x"}, "ref contains invalid characters"},
		{"git_show option commit", "git_show",
			map[string]any{"repo": repoA, "ref": "main", "commit": "--output=/tmp/x"}, "ref contains invalid characters"},
		{"git_show reflog syntax", "git_show",
			map[string]any{"repo": repoA, "ref": "main", "commit": "main@{0}"}, "ref contains invalid characters"},
		{"git_diff option base", "git_diff",
			map[string]any{"repo": repoA, "base": "--output=/tmp/x", "head": "main"}, "ref contains invalid characters"},
		{"git_diff range head", "git_diff",
			map[string]any{"repo": repoA, "base": "main", "head": "main...v1.0.0"}, "ref contains '..'"},
		{"git_blame traversal", "git_blame",
			map[string]any{"repo": repoA, "ref": "main", "path": "../../etc/passwd"}, "path contains '..'"},
		{"git_blame option path", "git_blame",
			map[string]any{"repo": repoA, "ref": "main", "path": "--contents=/etc/passwd"}, "path must not start with -"},
		{"resolve_ref range", "resolve_ref",
			map[string]any{"repo": repoA, "ref": "main..v1.0.0"}, "ref contains '..'"},
		{"resolve_ref short hex", "resolve_ref",
			map[string]any{"repo": repoA, "ref": "abc"}, "invalid length"},
		{"repo traversal inside allowed org", "resolve_ref",
			map[string]any{"repo": "git.test/org/x/../repo-b", "ref": "main"}, "access denied"},
		{"repo traversal to other org", "resolve_ref",
			map[string]any{"repo": "git.test/org/../other/secret", "ref": "main"}, "access denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, call(t, session, tt.tool, tt.args, nil), tt.wantErr)
		})
	}

	// The server must still work after the rejected calls.
	var ref tools.ResolveRefOutput
	require.Empty(t, call(t, session, "resolve_ref", map[string]any{"repo": repoA, "ref": "main"}, &ref))
	assert.Len(t, ref.SHA, 40)
}

func TestCache_EvictsLeastRecentlyUsedRepo(t *testing.T) {
	cacheDir := os.Getenv("CACHE_DIR")
	if cacheDir == "" {
		t.Skip("CACHE_DIR not set")
	}
	cached := func(repo string) bool {
		_, err := os.Stat(filepath.Join(cacheDir, repo+".git", "HEAD"))
		return err == nil
	}
	session := connect(t, "reader")
	resolve := func(repo string) {
		t.Helper()
		var out tools.ResolveRefOutput
		require.Empty(t, call(t, session, "resolve_ref", map[string]any{"repo": repo, "ref": "main"}, &out))
		require.Len(t, out.SHA, 40)
	}

	// max_cache_size fits one repo.
	resolve(repoA)
	assert.True(t, cached(repoA))

	resolve(repoB)
	assert.True(t, cached(repoB))
	assert.False(t, cached(repoA), "repo-a should be evicted")

	resolve(repoA)
	assert.True(t, cached(repoA), "repo-a should be cloned again")
	assert.False(t, cached(repoB), "repo-b should be evicted")
}

func TestErrors_HaveStableCodes(t *testing.T) {
	reader := connect(t, "reader")
	narrow := connect(t, "narrow")

	tests := []struct {
		name     string
		session  *mcp.ClientSession
		args     map[string]any
		wantCode string
	}{
		{"unknown ref", reader, map[string]any{"repo": repoA, "ref": "no-such-branch"}, "ref_not_found"},
		{"repo missing upstream", reader, map[string]any{"repo": "git.test/org/no-such-repo", "ref": "main"}, "upstream_error"},
		{"no upstream for host", reader, map[string]any{"repo": "unknown.test/org/repo", "ref": "main"}, "access_denied"},
		{"access denied", narrow, map[string]any{"repo": repoB, "ref": "main"}, "access_denied"},
		{"invalid ref", reader, map[string]any{"repo": repoA, "ref": "--x"}, "invalid_input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			res, err := tt.session.CallTool(ctx, &mcp.CallToolParams{Name: "resolve_ref", Arguments: tt.args})
			require.NoError(t, err)
			require.True(t, res.IsError)
			assert.Equal(t, tt.wantCode, res.Meta["error_code"])
			text := res.Content[0].(*mcp.TextContent).Text
			assert.True(t, strings.HasPrefix(text, tt.wantCode+": "), text)
		})
	}
}

func TestUpstream_HTTPCredentials(t *testing.T) {
	session := connect(t, "reader")

	var ref tools.ResolveRefOutput
	require.Empty(t, call(t, session, "resolve_ref", map[string]any{"repo": "private.test/org/repo", "ref": "main"}, &ref),
		"clone with username and password_env")
	assert.Len(t, ref.SHA, 40)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "resolve_ref", Arguments: map[string]any{"repo": "nocreds.test/org/repo", "ref": "main"}})
	require.NoError(t, err)
	require.True(t, res.IsError, "clone without credentials must fail")
	assert.Equal(t, "upstream_error", res.Meta["error_code"])
}
