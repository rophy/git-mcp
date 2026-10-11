package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	repoDir := setupBareRepo(t)
	exec := git.NewExecutor(1 * time.Millisecond)

	_, err := exec.Run(context.Background(), repoDir, "rev-parse", "--verify", "HEAD")
	assert.Error(t, err)
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
