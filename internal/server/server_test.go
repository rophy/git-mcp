package server_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

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
