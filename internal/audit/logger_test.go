package audit_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rophy/git-mcp/internal/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLog_StructuredJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := audit.NewLogger(slog.New(slog.NewJSONHandler(&buf, nil)))

	logger.Log(audit.Entry{
		Service:     "diagnostics-agent",
		RequestID:   "req-abc123",
		Tool:        "read_file",
		Repo:        "github.com/org/checkout-service",
		Ref:         "v2.7.0",
		ResolvedSHA: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		Path:        "src/handler.ts",
		Duration:    12 * time.Millisecond,
		Error:       "",
	})

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))
	assert.Equal(t, "diagnostics-agent", record["service"])
	assert.Equal(t, "req-abc123", record["request_id"])
	assert.Equal(t, "read_file", record["tool"])
	assert.Equal(t, "github.com/org/checkout-service", record["repo"])
	assert.Equal(t, "src/handler.ts", record["path"])
	assert.Equal(t, float64(12), record["duration_ms"])
}

func TestLog_ErrorField(t *testing.T) {
	var buf bytes.Buffer
	logger := audit.NewLogger(slog.New(slog.NewJSONHandler(&buf, nil)))

	logger.Log(audit.Entry{
		Service:  "sre-bot",
		Tool:     "search_code",
		Repo:     "github.com/org/api",
		Duration: 5 * time.Millisecond,
		Error:    "invalid ref",
	})

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))
	assert.Equal(t, "invalid ref", record["error"])
}

func TestLog_OmitsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	logger := audit.NewLogger(slog.New(slog.NewJSONHandler(&buf, nil)))

	logger.Log(audit.Entry{
		Service:  "sre-bot",
		Tool:     "resolve_ref",
		Repo:     "github.com/org/api",
		Ref:      "main",
		Duration: 3 * time.Millisecond,
	})

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))
	_, hasPath := record["path"]
	assert.False(t, hasPath, "path should be omitted when empty")
	_, hasError := record["error"]
	assert.False(t, hasError, "error should be omitted when empty")
}

func TestMiddleware_RequestID(t *testing.T) {
	cases := map[string]struct {
		header, value, want string
	}{
		"x-request-id": {"X-Request-Id", "req-1", "req-1"},
		"traceparent": {"Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			"4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7"},
		"none": {"", "", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var got string
			h := audit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = audit.RequestIDFromContext(r.Context())
			}))
			req := httptest.NewRequest("GET", "/", nil)
			if c.header != "" {
				req.Header.Set(c.header, c.value)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestContextWithService(t *testing.T) {
	ctx := audit.ContextWithService(httptest.NewRequest("GET", "/", nil).Context(), "svc")
	assert.Equal(t, "svc", audit.ServiceFromContext(ctx))
}
