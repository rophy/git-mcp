//go:build integration

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_FullWorkflow(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	env := []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	}

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%v: %s", args, out)
	}

	run("git", "init", "-b", "main")
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "pkg/handler.go"),
		[]byte("package pkg\n\nfunc Handle(err error) string {\n\treturn err.Error()\n}\n"), 0o644))
	run("git", "add", ".")
	run("git", "commit", "-m", "add handler")
	run("git", "tag", "v1.0.0")

	bareDir := filepath.Join(dir, "upstream.git")
	cmd := exec.Command("git", "clone", "--bare", workDir, bareDir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "bare clone: %s", out)

	cacheDir := filepath.Join(dir, "cache")
	executor := git.NewExecutor(30 * time.Second)
	mgr := cache.NewManager(cacheDir, executor, func(r string) (string, string, error) {
		return bareDir, "", nil
	})

	ctx := context.Background()
	repo := "local/test/repo"

	// 1. resolve_ref
	resolve := tools.NewResolveRef(mgr)
	refResult, err := resolve.Execute(ctx, tools.ResolveRefInput{Repo: repo, Ref: "v1.0.0"})
	require.NoError(t, err)
	sha := refResult.SHA

	// 2. search_code
	search := tools.NewSearchCode(mgr, executor, 50, 1024*1024)
	searchResult, err := search.Execute(ctx, tools.SearchCodeInput{Repo: repo, Ref: sha, Pattern: "Handle"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(searchResult.Matches), 1)

	// 3. read_file
	read := tools.NewReadFile(mgr, executor, 1024*1024)
	readResult, err := read.Execute(ctx, tools.ReadFileInput{Repo: repo, Ref: sha, Path: "pkg/handler.go"})
	require.NoError(t, err)
	assert.Contains(t, readResult.Content, "func Handle")

	// 4. list_files
	list := tools.NewListFiles(mgr, executor, 1024*1024)
	listResult, err := list.Execute(ctx, tools.ListFilesInput{Repo: repo, Ref: sha})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(listResult.Entries), 1)

	// 5. git_log
	log := tools.NewGitLog(mgr, executor, 1024*1024)
	logResult, err := log.Execute(ctx, tools.GitLogInput{Repo: repo, Ref: sha, Count: 10})
	require.NoError(t, err)
	assert.Equal(t, "add handler", logResult.Commits[0].Message)

	// 6. git_show
	show := tools.NewGitShow(mgr, executor, 500*1024)
	showResult, err := show.Execute(ctx, tools.GitShowInput{Repo: repo, Ref: sha, Commit: sha})
	require.NoError(t, err)
	assert.Equal(t, "add handler", showResult.Message)
	assert.Contains(t, showResult.Diff, "handler.go")
}
