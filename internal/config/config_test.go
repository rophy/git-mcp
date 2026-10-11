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

func TestUpstreamURL(t *testing.T) {
	cfg := &config.Config{Upstream: map[string]config.UpstreamConfig{
		"github.com": {Token: "tok"},
		"git.test":   {BaseURL: "http://gitserver:8080/"},
	}}

	tests := []struct {
		name      string
		repo      string
		wantURL   string
		wantToken string
		wantErr   string
	}{
		{"default https", "github.com/org/repo", "https://github.com/org/repo", "tok", ""},
		{"base_url replaces host", "git.test/org/repo", "http://gitserver:8080/org/repo", "", ""},
		{"unknown host", "gitlab.com/org/repo", "", "", `no upstream configured for host "gitlab.com"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, token, err := cfg.UpstreamURL(tt.repo)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, url)
			assert.Equal(t, tt.wantToken, token)
		})
	}
}

func TestInsecureUpstreams(t *testing.T) {
	cfg := &config.Config{Upstream: map[string]config.UpstreamConfig{
		"github.com": {Token: "tok"},
		"plain.test": {BaseURL: "http://plain"},
		"leaky.test": {BaseURL: "HTTP://leaky", Token: "tok"},
		"tls.test":   {BaseURL: "https://tls", Token: "tok"},
	}}
	assert.Equal(t, []string{"leaky.test"}, cfg.InsecureUpstreams())
}

func TestServiceAllowsRepo_RejectsUncleanPath(t *testing.T) {
	wild := config.ServiceConfig{Repos: []string{"github.com/org/*"}}
	assert.False(t, wild.AllowsRepo("github.com/org/x/../repo-b"))
	assert.False(t, wild.AllowsRepo("github.com/org/./repo-b"))
	assert.False(t, wild.AllowsRepo("github.com/org//repo-b"))
	assert.True(t, wild.AllowsRepo("github.com/org/repo-b"))
}
