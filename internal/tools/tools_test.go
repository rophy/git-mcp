package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRepo(t *testing.T) (cacheManager *cache.Manager, repo string, headSHA string) {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
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
		return strings.TrimSpace(string(out))
	}

	run("git", "init", "-b", "main")
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "src/main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Test\n"), 0o644))
	run("git", "add", ".")
	run("git", "-c", "commit.gpgsign=false", "commit", "-m", "initial commit")
	sha := run("git", "rev-parse", "HEAD")

	bareDir := filepath.Join(dir, "upstream.git")
	cmd := exec.Command("git", "clone", "--bare", workDir, bareDir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "bare clone: %s", out)

	cacheDir := filepath.Join(dir, "cache")
	executor := git.NewExecutor(30 * time.Second)
	mgr := cache.NewManager(cacheDir, executor, func(r string) (string, string, error) {
		return bareDir, "", nil
	})

	return mgr, "test/org/repo", sha
}

func TestResolveRef(t *testing.T) {
	mgr, repo, expectedSHA := setupTestRepo(t)
	handler := tools.NewResolveRef(mgr)

	result, err := handler.Execute(context.Background(), tools.ResolveRefInput{
		Repo: repo, Ref: "main", Refresh: false,
	})
	require.NoError(t, err)
	assert.Equal(t, expectedSHA, result.SHA)
}

func TestReadFile(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewReadFile(mgr, executor, 1024*1024)

	result, err := handler.Execute(context.Background(), tools.ReadFileInput{
		Repo: repo, Ref: "main", Path: "src/main.go",
	})
	require.NoError(t, err)
	assert.Contains(t, result.Content, "package main")
	assert.Contains(t, result.Content, "1\t")
	assert.NotEmpty(t, result.SHA)
}

func TestReadFile_LineRange(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewReadFile(mgr, executor, 1024*1024)

	start, end := 3, 4
	result, err := handler.Execute(context.Background(), tools.ReadFileInput{
		Repo: repo, Ref: "main", Path: "src/main.go",
		StartLine: &start, EndLine: &end,
	})
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(result.Content, "\n"), "\n")
	assert.Len(t, lines, 2)
	assert.True(t, strings.HasPrefix(lines[0], "3\t"))
}

func TestSearchCode(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewSearchCode(mgr, executor, 50)

	result, err := handler.Execute(context.Background(), tools.SearchCodeInput{
		Repo: repo, Ref: "main", Pattern: "hello",
	})
	require.NoError(t, err)
	assert.Len(t, result.Matches, 1)
	assert.Equal(t, "src/main.go", result.Matches[0].Path)
}

func TestSearchCode_NoMatch(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewSearchCode(mgr, executor, 50)

	result, err := handler.Execute(context.Background(), tools.SearchCodeInput{
		Repo: repo, Ref: "main", Pattern: "zzzznomatch",
	})
	require.NoError(t, err)
	assert.Empty(t, result.Matches)
}

func TestListFiles(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewListFiles(mgr, executor)

	result, err := handler.Execute(context.Background(), tools.ListFilesInput{
		Repo: repo, Ref: "main",
	})
	require.NoError(t, err)
	names := make([]string, len(result.Entries))
	for i, e := range result.Entries {
		names[i] = e.Name
	}
	assert.Contains(t, names, "README.md")
	assert.Contains(t, names, "src")
}

func TestGitLog(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewGitLog(mgr, executor)

	result, err := handler.Execute(context.Background(), tools.GitLogInput{
		Repo: repo, Ref: "main", Count: 10,
	})
	require.NoError(t, err)
	assert.Len(t, result.Commits, 1)
	assert.Equal(t, "initial commit", result.Commits[0].Message)
}

func TestGitShow(t *testing.T) {
	mgr, repo, headSHA := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewGitShow(mgr, executor, 500*1024)

	result, err := handler.Execute(context.Background(), tools.GitShowInput{
		Repo: repo, Ref: "main", Commit: headSHA,
	})
	require.NoError(t, err)
	assert.Equal(t, "initial commit", result.Message)
	assert.Contains(t, result.Diff, "main.go")
}
