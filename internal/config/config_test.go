package config_test

import (
	"testing"
	"time"

	"github.com/rophy/git-mcp/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	cfg, err := config.Load("../../testdata/config.yaml")
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1:8080", cfg.Server.Listen)
	assert.Equal(t, "/tmp/git-mcp-test/repos", cfg.Server.CacheDir)
	assert.Equal(t, 50, cfg.Server.MaxMatches)
	assert.Equal(t, 30*time.Second, cfg.Server.GitTimeout)

	assert.Equal(t, "ghp_test_token", cfg.Upstream["github.com"].Token)
	assert.Equal(t, "git-mcp", cfg.Auth.Audience)
	assert.Len(t, cfg.Auth.Issuers, 1)
	assert.Equal(t, "https://oauth.example.com", cfg.Auth.Issuers[0].Iss)

	svc, ok := cfg.Services["test-service"]
	require.True(t, ok)
	assert.Equal(t, "test-oauth", svc.Issuer)
	assert.Equal(t, "test-client-id", svc.Subject)
	assert.Equal(t, []string{"github.com/org/repo-a", "github.com/org/repo-b"}, svc.Repos)
}

func TestLoadConfig_EnvExpansion(t *testing.T) {
	t.Setenv("TEST_TOKEN", "expanded-token")
	cfg, err := config.Load("../../testdata/config_env.yaml")
	require.NoError(t, err)
	assert.Equal(t, "expanded-token", cfg.Upstream["github.com"].Token)
}

func TestServiceAllowsRepo(t *testing.T) {
	cfg, err := config.Load("../../testdata/config.yaml")
	require.NoError(t, err)

	svc := cfg.Services["test-service"]
	assert.True(t, svc.AllowsRepo("github.com/org/repo-a"))
	assert.False(t, svc.AllowsRepo("github.com/org/repo-c"))

	wild := cfg.Services["wildcard-service"]
	assert.True(t, wild.AllowsRepo("github.com/org/anything"))
	assert.False(t, wild.AllowsRepo("github.com/other/repo"))
}
