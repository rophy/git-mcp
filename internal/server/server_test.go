package server_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rophy/git-mcp/internal/config"
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
	}, got)
}
