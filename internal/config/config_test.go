package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rophy/git-mcp/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("TEST_UPSTREAM_PASSWORD", "s3cret")
	cfg, err := config.Load("../../testdata/config.yaml")
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1:8080", cfg.Server.Listen)
	assert.Equal(t, "/tmp/git-mcp-test/repos", cfg.Server.CacheDir)
	assert.Equal(t, 50, cfg.Server.MaxMatches)
	assert.Equal(t, 30*time.Second, cfg.Server.GitTimeout)

	assert.Equal(t, "git-mcp", cfg.Upstream["github.com"].Username)
	assert.Equal(t, "s3cret", cfg.Upstream["github.com"].Password, "password is read from password_env")
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
	t.Setenv("TEST_BASE_URL", "https://git.example.test")
	cfg, err := config.Load("../../testdata/config_env.yaml")
	require.NoError(t, err)
	assert.Equal(t, "https://git.example.test", cfg.Upstream["github.com"].BaseURL)
}

func TestServiceAllowsRepo(t *testing.T) {
	t.Setenv("TEST_UPSTREAM_PASSWORD", "s3cret")
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
		"github.com": {Username: "u", Password: "p"},
		"git.test":   {BaseURL: "http://gitserver:8080/"},
	}}

	tests := []struct {
		name     string
		repo     string
		wantURL  string
		wantUser string
		wantPass string
		wantErr  string
	}{
		{"default https", "github.com/org/repo", "https://github.com/org/repo", "u", "p", ""},
		{"base_url replaces host", "git.test/org/repo", "http://gitserver:8080/org/repo", "", "", ""},
		{"unknown host", "gitlab.com/org/repo", "", "", "", `no upstream configured for host "gitlab.com"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, user, pass, err := cfg.UpstreamURL(tt.repo)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, url)
			assert.Equal(t, tt.wantUser, user)
			assert.Equal(t, tt.wantPass, pass)
		})
	}
}

func TestInsecureUpstreams(t *testing.T) {
	cfg := &config.Config{Upstream: map[string]config.UpstreamConfig{
		"github.com": {Username: "u", Password: "p"},
		"plain.test": {BaseURL: "http://plain"},
		"leaky.test": {BaseURL: "HTTP://leaky", Username: "u", Password: "p"},
		"tls.test":   {BaseURL: "https://tls", Username: "u", Password: "p"},
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

func TestParseSize(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"512", 512, false},
		{"500KB", 500 * 1024, false},
		{"1MB", 1024 * 1024, false},
		{"50GB", 50 * 1024 * 1024 * 1024, false},
		{"2gb", 2 * 1024 * 1024 * 1024, false},
		{" 1 MB ", 1024 * 1024, false},
		{"1TB", 0, true},
		{"abc", 0, true},
		{"-1MB", 0, true},
		{"1.5GB", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := config.ParseSize(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLoadConfig_SizeDefaultsAndParsing(t *testing.T) {
	cfg, err := config.Load("../../testdata/config_env.yaml")
	require.NoError(t, err)
	assert.Equal(t, int64(0), cfg.Server.MaxCacheBytes)
	assert.Equal(t, 1024*1024, cfg.Server.MaxFileBytes)
	assert.Equal(t, 500*1024, cfg.Server.MaxDiffBytes)
	assert.Equal(t, 1024*1024, cfg.Server.MaxOutputBytes)

	t.Setenv("TEST_UPSTREAM_PASSWORD", "s3cret")
	cfg, err = config.Load("../../testdata/config.yaml")
	require.NoError(t, err)
	assert.Equal(t, int64(1024*1024*1024), cfg.Server.MaxCacheBytes)
}

func TestLoadConfig_InvalidSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server:\n  max_cache_size: \"50XB\"\n"), 0o644))
	_, err := config.Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_cache_size")
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestLoadConfig_RejectsSecretsInFile(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"token field", "upstream:\n  github.com:\n    token: \"ghp_abc\"\n", "password_env"},
		{"password field", "upstream:\n  github.com:\n    password: \"abc\"\n", "password_env"},
		{"credentials in base_url", "upstream:\n  git.test:\n    base_url: \"https://u:p@git.test\"\n", "base_url must not contain credentials"},
		{"username in base_url", "upstream:\n  git.test:\n    base_url: \"https://u@git.test\"\n", "base_url must not contain credentials"},
		{"unknown field", "server:\n  listne: \"x\"\n", "listne"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, tt.body))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoadConfig_PasswordEnv(t *testing.T) {
	body := "upstream:\n  git.test:\n    username: \"bot\"\n    password_env: \"GIT_TEST_PW\"\n"

	t.Run("reads the env var", func(t *testing.T) {
		t.Setenv("GIT_TEST_PW", "s3cret")
		cfg, err := config.Load(writeConfig(t, body))
		require.NoError(t, err)
		_, user, pass, err := cfg.UpstreamURL("git.test/org/repo")
		require.NoError(t, err)
		assert.Equal(t, "bot", user)
		assert.Equal(t, "s3cret", pass)
	})

	t.Run("empty env var fails", func(t *testing.T) {
		t.Setenv("GIT_TEST_PW", "")
		_, err := config.Load(writeConfig(t, body))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GIT_TEST_PW")
	})

	t.Run("password_env needs username", func(t *testing.T) {
		t.Setenv("GIT_TEST_PW", "s3cret")
		_, err := config.Load(writeConfig(t, "upstream:\n  git.test:\n    password_env: \"GIT_TEST_PW\"\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "username")
	})
}
