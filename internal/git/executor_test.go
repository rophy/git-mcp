package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rophy/git-mcp/internal/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "test.git")

	workDir := filepath.Join(dir, "work")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	cmds := [][]string{
		{"git", "init", workDir},
		{"git", "-C", workDir, "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "cmd %v failed: %s", args, out)
	}

	cmd := exec.Command("git", "clone", "--bare", workDir, repoDir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "bare clone failed: %s", out)

	return repoDir
}

func TestExecutorRun(t *testing.T) {
	repoDir := setupBareRepo(t)
	exec := git.NewExecutor(30 * time.Second)

	out, err := exec.Run(context.Background(), repoDir, "rev-parse", "--verify", "HEAD")
	require.NoError(t, err)
	assert.Len(t, out, 40) // full SHA
}

func TestExecutorTimeout(t *testing.T) {
	exec := git.NewExecutor(50 * time.Millisecond)

	// Clone a non-routable IP to guarantee the timeout fires
	_, err := exec.Run(context.Background(), t.TempDir(), "clone", "https://192.0.2.1/repo.git", "out")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestExecutorSanitizesEnv(t *testing.T) {
	repoDir := setupBareRepo(t)
	t.Setenv("GIT_DIR", "/should/be/removed")
	t.Setenv("GIT_WORK_TREE", "/should/be/removed")

	exec := git.NewExecutor(30 * time.Second)
	out, err := exec.Run(context.Background(), repoDir, "rev-parse", "--verify", "HEAD")
	require.NoError(t, err)
	assert.Len(t, out, 40)
}

func TestExecutorRunLimited_UnderLimit(t *testing.T) {
	repoDir := setupBareRepo(t)
	exec := git.NewExecutor(30 * time.Second)

	out, truncated, err := exec.RunLimited(context.Background(), repoDir, 100, "rev-parse", "--verify", "HEAD")
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Len(t, out, 41, "output is not trimmed")
	assert.True(t, strings.HasSuffix(out, "\n"))
}

func TestExecutorRunLimited_OverLimit(t *testing.T) {
	repoDir := setupBareRepo(t)
	exec := git.NewExecutor(30 * time.Second)

	out, truncated, err := exec.RunLimited(context.Background(), repoDir, 10, "rev-parse", "--verify", "HEAD")
	require.NoError(t, err)
	assert.True(t, truncated)
	assert.Len(t, out, 10)
}

func TestExecutorRunLimited_StopsLargeOutput(t *testing.T) {
	repoDir := setupBareRepo(t)
	exec := git.NewExecutor(30 * time.Second)

	bigFile := filepath.Join(t.TempDir(), "big")
	require.NoError(t, os.WriteFile(bigFile, []byte(strings.Repeat("x", 1<<20)), 0o644))
	blob, err := exec.Run(context.Background(), repoDir, "hash-object", "-w", bigFile)
	require.NoError(t, err)

	start := time.Now()
	out, truncated, err := exec.RunLimited(context.Background(), repoDir, 1024, "cat-file", "blob", blob)
	require.NoError(t, err)
	assert.True(t, truncated)
	assert.Len(t, out, 1024)
	assert.Less(t, time.Since(start), 5*time.Second)
}
