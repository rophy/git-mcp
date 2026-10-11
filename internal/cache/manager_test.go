package cache_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupUpstreamRepo(t *testing.T) (upstreamPath string, expectedSHA string) {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "upstream")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	env := []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	}

	run := func(args ...string) string {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "cmd %v: %s", args, out)
		return string(out)
	}

	run("git", "init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello\n"), 0o644))
	run("git", "add", ".")
	run("git", "commit", "-m", "initial")
	sha := run("git", "rev-parse", "HEAD")

	bareDir := filepath.Join(dir, "upstream.git")
	cmd := exec.Command("git", "clone", "--bare", workDir, bareDir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "bare clone: %s", out)

	return bareDir, sha[:40]
}

func TestEnsure_CloneAndResolve(t *testing.T) {
	upstreamPath, expectedSHA := setupUpstreamRepo(t)
	cacheDir := t.TempDir()

	executor := git.NewExecutor(30 * time.Second)
	mgr := cache.NewManager(cacheDir, executor, func(repo string) (string, string, error) {
		return upstreamPath, "", nil
	})

	sha, err := mgr.Ensure(context.Background(), "local/test/repo", "main", false)
	require.NoError(t, err)
	assert.Equal(t, expectedSHA, sha)
}

func TestEnsure_CachedResolve(t *testing.T) {
	upstreamPath, expectedSHA := setupUpstreamRepo(t)
	cacheDir := t.TempDir()

	executor := git.NewExecutor(30 * time.Second)
	mgr := cache.NewManager(cacheDir, executor, func(repo string) (string, string, error) {
		return upstreamPath, "", nil
	})

	sha1, err := mgr.Ensure(context.Background(), "local/test/repo", "main", false)
	require.NoError(t, err)

	sha2, err := mgr.Ensure(context.Background(), "local/test/repo", sha1, false)
	require.NoError(t, err)
	assert.Equal(t, expectedSHA, sha2)
}

func TestEnsure_RepoPath(t *testing.T) {
	upstreamPath, _ := setupUpstreamRepo(t)
	cacheDir := t.TempDir()

	executor := git.NewExecutor(30 * time.Second)
	mgr := cache.NewManager(cacheDir, executor, func(repo string) (string, string, error) {
		return upstreamPath, "", nil
	})

	_, err := mgr.Ensure(context.Background(), "github.com/org/repo", "main", false)
	require.NoError(t, err)

	expectedPath := filepath.Join(cacheDir, "github.com", "org", "repo.git")
	_, err = os.Stat(expectedPath)
	assert.NoError(t, err, "bare repo should exist at %s", expectedPath)
}
