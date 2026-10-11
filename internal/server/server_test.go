package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rophy/git-mcp/internal/auth"
	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/config"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunHTTP_WarnsWhenAuthDisabled(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	srv := server.New(&config.Config{}, nil, nil, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, server.RunHTTP(ctx, srv, "127.0.0.1:0", nil, logger))

	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "authentication disabled")
}

func TestNew_ToolSchemasRequireOnlyMandatoryInputs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	srv := server.New(&config.Config{}, nil, nil, nil, logger)

	ctx := t.Context()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	got := map[string][]string{}
	for _, tool := range res.Tools {
		schema, ok := tool.InputSchema.(map[string]any)
		require.True(t, ok, tool.Name)
		var required []string
		if list, ok := schema["required"].([]any); ok {
			for _, r := range list {
				required = append(required, r.(string))
			}
		}
		got[tool.Name] = required
	}

	assert.Equal(t, map[string][]string{
		"resolve_ref": {"repo", "ref"},
		"search_code": {"repo", "ref", "pattern"},
		"read_file":   {"repo", "ref", "path"},
		"list_files":  {"repo", "ref"},
		"git_log":     {"repo", "ref"},
		"git_show":    {"repo", "ref", "commit"},
		"git_diff":    {"repo", "base", "head"},
		"git_blame":   {"repo", "ref", "path"},
	}, got)
}

func TestServeHTTP_WaitsForInFlightRequests(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	started := make(chan struct{})
	var finished atomic.Bool

	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "slow"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		close(started)
		time.Sleep(500 * time.Millisecond)
		finished.Store(true)
		return &mcp.CallToolResult{}, struct{}{}, nil
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- server.ServeHTTP(ctx, srv, ln, nil, logger) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: "http://" + ln.Addr().String() + "/mcp"}, nil)
	require.NoError(t, err)
	defer session.Close()

	called := make(chan error, 1)
	go func() {
		_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "slow"})
		called <- err
	}()

	<-started
	cancel()
	require.NoError(t, <-served)
	assert.True(t, finished.Load(), "ServeHTTP returned before the in-flight request finished")
	assert.NoError(t, <-called)
}

// newTestServer returns a server backed by a local one-commit repo, the repo
// name, its HEAD SHA, and the buffer that receives JSON logs.
func newTestServer(t *testing.T, authenticator *auth.Authenticator) (*mcp.Server, string, string, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	require.NoError(t, os.MkdirAll(work, 0o755))
	run("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")
	sha := run("rev-parse", "HEAD")

	executor := git.NewExecutor(30 * time.Second)
	mgr, err := cache.NewManager(filepath.Join(dir, "cache"), 0, executor, func(string) (string, string, error) {
		return work, "", nil
	})
	require.NoError(t, err)

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	return server.New(&config.Config{}, mgr, executor, authenticator, logger), "h/o/repo", sha, logs
}

func connectInMemory(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err := srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })
	return session
}

// auditLines returns the "tool_call" records written to logs.
func auditLines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == "tool_call" {
			records = append(records, record)
		}
	}
	return records
}

func TestAudit_LogsEachToolCall(t *testing.T) {
	srv, repo, sha, logs := newTestServer(t, nil)
	session := connectInMemory(t, srv)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "resolve_ref", Arguments: map[string]any{"repo": repo, "ref": "main"}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "read_file", Arguments: map[string]any{"repo": repo, "ref": "main", "path": "../x"}})
	require.NoError(t, err)
	require.True(t, res.IsError)

	records := auditLines(t, logs)
	require.Len(t, records, 2)

	ok := records[0]
	assert.Equal(t, "resolve_ref", ok["tool"])
	assert.Equal(t, repo, ok["repo"])
	assert.Equal(t, "main", ok["ref"])
	assert.Equal(t, sha, ok["resolved_sha"])
	assert.Contains(t, ok, "duration_ms")
	assert.NotContains(t, ok, "error")

	failed := records[1]
	assert.Equal(t, "read_file", failed["tool"])
	assert.Equal(t, "../x", failed["path"])
	assert.Contains(t, failed["error"], "path contains '..'")
}

type headerTransport struct{ key, value string }

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(h.key, h.value)
	return http.DefaultTransport.RoundTrip(r)
}

func TestAudit_LogsRequestIDFromHTTPHeader(t *testing.T) {
	srv, repo, _, logs := newTestServer(t, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.ServeHTTP(ctx, srv, ln, nil, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   "http://" + ln.Addr().String() + "/mcp",
		HTTPClient: &http.Client{Transport: headerTransport{"X-Request-Id", "req-123"}},
	}, nil)
	require.NoError(t, err)
	defer session.Close()

	_, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "resolve_ref", Arguments: map[string]any{"repo": repo, "ref": "main"}})
	require.NoError(t, err)

	records := auditLines(t, logs)
	require.Len(t, records, 1)
	assert.Equal(t, "req-123", records[0]["request_id"])
}

func TestAccess_FailsClosedWithoutService(t *testing.T) {
	authenticator, err := auth.NewAuthenticator(t.Context(), &config.Config{Auth: config.AuthConfig{
		Audience: "git-mcp",
		Issuers:  []config.IssuerConfig{{Name: "i", Iss: "https://issuer.test", JWKSURI: "http://127.0.0.1:1/jwks.json"}},
	}})
	require.NoError(t, err)
	srv, repo, _, logs := newTestServer(t, authenticator)
	session := connectInMemory(t, srv)

	// No HTTP auth middleware ran, so the context has no service.
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "resolve_ref", Arguments: map[string]any{"repo": repo, "ref": "main"}})
	require.NoError(t, err)
	assert.True(t, res.IsError, "call without an authenticated service must be denied")
	code, _ := toolError(t, res)
	assert.Equal(t, "access_denied", code)

	records := auditLines(t, logs)
	require.Len(t, records, 1)
	assert.Contains(t, records[0]["error"], "access denied")
}

func TestServeHTTP_ShutdownWithIdleNewConnection(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- server.ServeHTTP(ctx, srv, ln, nil, logger) }()

	// A connection that never sends a request, e.g. a client's spare dial.
	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	cancel()
	err = <-served
	assert.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second, "shutdown must not wait for connections that sent no request")
}

// toolError returns the error code from _meta and the text of an error result.
func toolError(t *testing.T, res *mcp.CallToolResult) (string, string) {
	t.Helper()
	require.True(t, res.IsError, "expected an error result")
	require.Len(t, res.Content, 1)
	text := res.Content[0].(*mcp.TextContent).Text
	code, _ := res.Meta["error_code"].(string)
	return code, text
}

func TestErrors_HaveStableCodes(t *testing.T) {
	srv, repo, _, logs := newTestServer(t, nil)
	session := connectInMemory(t, srv)

	tests := []struct {
		tool     string
		args     map[string]any
		wantCode string
	}{
		{"read_file", map[string]any{"repo": repo, "ref": "main", "path": "../x"}, "invalid_input"},
		{"search_code", map[string]any{"repo": repo, "ref": "main", "pattern": "-x"}, "invalid_input"},
		{"resolve_ref", map[string]any{"repo": repo, "ref": "no-such-branch"}, "ref_not_found"},
	}
	for _, tt := range tests {
		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tt.tool, Arguments: tt.args})
		require.NoError(t, err, "a tool error must be a result, not a protocol error")
		code, text := toolError(t, res)
		assert.Equal(t, tt.wantCode, code, text)
		assert.True(t, strings.HasPrefix(text, tt.wantCode+": "), text)
	}

	records := auditLines(t, logs)
	require.Len(t, records, len(tests))
	for i, tt := range tests {
		assert.Equal(t, tt.wantCode, records[i]["error_code"])
	}
}

func TestErrors_EveryToolReturnsToolError(t *testing.T) {
	srv, repo, _, _ := newTestServer(t, nil)
	session := connectInMemory(t, srv)

	// An invalid ref fails every tool after the SDK's schema check.
	argsByTool := map[string]map[string]any{
		"resolve_ref": {"repo": repo, "ref": "--x"},
		"search_code": {"repo": repo, "ref": "--x", "pattern": "a"},
		"read_file":   {"repo": repo, "ref": "--x", "path": "a.txt"},
		"list_files":  {"repo": repo, "ref": "--x"},
		"git_log":     {"repo": repo, "ref": "--x"},
		"git_show":    {"repo": repo, "ref": "--x", "commit": "--x"},
		"git_diff":    {"repo": repo, "base": "--x", "head": "--x"},
		"git_blame":   {"repo": repo, "ref": "--x", "path": "a.txt"},
	}
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, res.Tools, len(argsByTool), "add new tools to argsByTool")
	for _, tool := range res.Tools {
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tool.Name, Arguments: argsByTool[tool.Name]})
		require.NoError(t, err, "%s: a tool error must be a result, not a protocol error", tool.Name)
		code, text := toolError(t, result)
		assert.Equal(t, "invalid_input", code, "%s: %s", tool.Name, text)
	}
}
