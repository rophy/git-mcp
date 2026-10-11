package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// specialName is a path that git quotes unless -z is used. It also has a ':'.
const specialName = "docs/héllo \"q\":x.txt"

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
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "docs/utf8.txt"), []byte(strings.Repeat("é", 100)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, specialName), []byte("special\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "docs/blank.txt"), []byte("a\n\n\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "docs/no-eol.txt"), []byte("x\ny"), 0o644))
	run("git", "add", ".")
	run("git", "-c", "commit.gpgsign=false", "commit", "-m", "initial commit")
	sha := run("git", "rev-parse", "HEAD")

	// Branches "other" and "feature" each add one file on top of main.
	for _, branch := range []string{"other", "feature"} {
		run("git", "checkout", "-q", "-b", branch, "main")
		require.NoError(t, os.WriteFile(filepath.Join(workDir, branch+".txt"), []byte(branch+" change\n"), 0o644))
		if branch == "feature" {
			require.NoError(t, os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Test\nfeature line\n"), 0o644))
		}
		run("git", "add", ".")
		run("git", "-c", "commit.gpgsign=false", "commit", "-m", "add "+branch)
	}
	run("git", "checkout", "-q", "main")

	bareDir := filepath.Join(dir, "upstream.git")
	cmd := exec.Command("git", "clone", "--bare", workDir, bareDir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "bare clone: %s", out)

	cacheDir := filepath.Join(dir, "cache")
	executor := git.NewExecutor(30 * time.Second)
	mgr, err := cache.NewManager(cacheDir, 0, executor, func(r string) (string, string, error) {
		return bareDir, "", nil
	})
	require.NoError(t, err)

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
	handler := tools.NewSearchCode(mgr, executor, 50, 1024*1024)

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
	handler := tools.NewSearchCode(mgr, executor, 50, 1024*1024)

	result, err := handler.Execute(context.Background(), tools.SearchCodeInput{
		Repo: repo, Ref: "main", Pattern: "zzzznomatch",
	})
	require.NoError(t, err)
	assert.Empty(t, result.Matches)
}

func TestListFiles(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewListFiles(mgr, executor, 1024*1024)

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
	handler := tools.NewGitLog(mgr, executor, 1024*1024)

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

func TestReadFile_TooLarge(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewReadFile(mgr, executor, 10)

	_, err := handler.Execute(context.Background(), tools.ReadFileInput{
		Repo: repo, Ref: "main", Path: "src/main.go",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds max size")
}

func TestGitShow_TruncatesAtRuneBoundary(t *testing.T) {
	mgr, repo, headSHA := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	input := tools.GitShowInput{Repo: repo, Ref: "main", Commit: headSHA}

	full, err := tools.NewGitShow(mgr, executor, 500*1024).Execute(context.Background(), input)
	require.NoError(t, err)
	idx := strings.Index(full.Diff, "é")
	require.Positive(t, idx)

	// Limit falls inside the 2-byte rune.
	result, err := tools.NewGitShow(mgr, executor, idx+1).Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.True(t, utf8.ValidString(result.Diff))
	assert.Equal(t, full.Diff[:idx], result.Diff)
}

func TestSearchCode_OutputLimit(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	input := tools.SearchCodeInput{Repo: repo, Ref: "main", Pattern: "."}

	full, err := tools.NewSearchCode(mgr, executor, 50, 1024*1024).Execute(context.Background(), input)
	require.NoError(t, err)
	require.Greater(t, len(full.Matches), 2)

	// Room for the first match line plus part of the second.
	result, err := tools.NewSearchCode(mgr, executor, 50, 150).Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	require.NotEmpty(t, result.Matches)
	assert.Equal(t, full.Matches[:len(result.Matches)], result.Matches)
}

func TestListFiles_OutputLimit(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	input := tools.ListFilesInput{Repo: repo, Ref: "main", Recursive: true}

	full, err := tools.NewListFiles(mgr, executor, 1024*1024).Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, full.Truncated)
	require.Greater(t, len(full.Entries), 1)

	// Room for the first ls-tree line plus part of the second.
	result, err := tools.NewListFiles(mgr, executor, 80).Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.Equal(t, full.Entries[:1], result.Entries)
}

func TestGitLog_OutputLimit(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)

	result, err := tools.NewGitLog(mgr, executor, 50).Execute(context.Background(), tools.GitLogInput{
		Repo: repo, Ref: "main",
	})
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.Empty(t, result.Commits)
}

func TestListFiles_SpecialCharacters(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewListFiles(mgr, executor, 1024*1024)

	result, err := handler.Execute(context.Background(), tools.ListFilesInput{
		Repo: repo, Ref: "main", Recursive: true,
	})
	require.NoError(t, err)
	assert.Contains(t, result.Entries, tools.FileEntry{Name: specialName, Type: "file"})
}

func TestSearchCode_SpecialCharacters(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewSearchCode(mgr, executor, 50, 1024*1024)

	result, err := handler.Execute(context.Background(), tools.SearchCodeInput{
		Repo: repo, Ref: "main", Pattern: "special",
	})
	require.NoError(t, err)
	assert.Equal(t, []tools.SearchMatch{{Path: specialName, LineNumber: 1, LineText: "special"}}, result.Matches)
}

func TestReadFile_KeepsTrailingBlankLines(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewReadFile(mgr, executor, 1024*1024)

	result, err := handler.Execute(context.Background(), tools.ReadFileInput{
		Repo: repo, Ref: "main", Path: "docs/blank.txt",
	})
	require.NoError(t, err)
	assert.Equal(t, "1\ta\n2\t\n3\t\n", result.Content)
}

func TestReadFile_NoTrailingNewline(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewReadFile(mgr, executor, 1024*1024)

	result, err := handler.Execute(context.Background(), tools.ReadFileInput{
		Repo: repo, Ref: "main", Path: "docs/no-eol.txt",
	})
	require.NoError(t, err)
	assert.Equal(t, "1\tx\n2\ty\n", result.Content)
}

func TestSearchCode_InvalidRegexIsError(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewSearchCode(mgr, executor, 50, 1024*1024)

	// git grep exits 128 on a bad regex; only exit 1 means "no match".
	_, err := handler.Execute(context.Background(), tools.SearchCodeInput{
		Repo: repo, Ref: "main", Pattern: "(",
	})
	require.Error(t, err)
}

func TestGitDiff(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewGitDiff(mgr, executor, 500*1024)

	result, err := handler.Execute(context.Background(), tools.GitDiffInput{
		Repo: repo, Base: "other", Head: "feature",
	})
	require.NoError(t, err)
	assert.Len(t, result.BaseSHA, 40)
	assert.Len(t, result.HeadSHA, 40)
	assert.NotEqual(t, result.BaseSHA, result.HeadSHA)
	assert.Contains(t, result.Diff, "+feature change")
	assert.Contains(t, result.Diff, "-other change", "direct diff also shows what only base has")
	assert.False(t, result.Truncated)
}

func TestGitDiff_MergeBase(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewGitDiff(mgr, executor, 500*1024)

	result, err := handler.Execute(context.Background(), tools.GitDiffInput{
		Repo: repo, Base: "other", Head: "feature", MergeBase: true,
	})
	require.NoError(t, err)
	assert.Contains(t, result.Diff, "+feature change")
	assert.NotContains(t, result.Diff, "other change", "merge-base diff shows only head's changes")
}

func TestGitDiff_Path(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewGitDiff(mgr, executor, 500*1024)

	result, err := handler.Execute(context.Background(), tools.GitDiffInput{
		Repo: repo, Base: "other", Head: "feature", Path: "feature.txt",
	})
	require.NoError(t, err)
	assert.Contains(t, result.Diff, "+feature change")
	assert.NotContains(t, result.Diff, "other.txt")
}

func TestGitDiff_Truncates(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewGitDiff(mgr, executor, 20)

	result, err := handler.Execute(context.Background(), tools.GitDiffInput{
		Repo: repo, Base: "other", Head: "feature",
	})
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.Len(t, result.Diff, 20)
}

func TestGitDiff_RejectsInvalidInput(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewGitDiff(mgr, executor, 500*1024)

	tests := []tools.GitDiffInput{
		{Repo: repo, Base: "--output=/tmp/x", Head: "main"},
		{Repo: repo, Base: "main", Head: "main..other"},
		{Repo: repo, Base: "main", Head: "other", Path: "../x"},
		{Repo: repo, Base: "", Head: "main"},
	}
	for _, in := range tests {
		_, err := handler.Execute(context.Background(), in)
		assert.Error(t, err, "%+v", in)
	}
}

func TestGitBlame(t *testing.T) {
	mgr, repo, initialSHA := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewGitBlame(mgr, executor, 1024*1024)

	result, err := handler.Execute(context.Background(), tools.GitBlameInput{
		Repo: repo, Ref: "feature", Path: "README.md",
	})
	require.NoError(t, err)
	featureSHA := result.SHA
	require.NotEqual(t, initialSHA, featureSHA)

	assert.Equal(t, []tools.BlameLine{
		{Line: 1, Commit: initialSHA, Text: "# Test"},
		{Line: 2, Commit: featureSHA, Text: "feature line"},
	}, result.Lines)
	require.Len(t, result.Commits, 2)
	assert.Equal(t, "initial commit", result.Commits[initialSHA].Summary)
	assert.Equal(t, "add feature", result.Commits[featureSHA].Summary)
	assert.Equal(t, "test <test@test.com>", result.Commits[featureSHA].Author)
	_, err = time.Parse(time.RFC3339, result.Commits[featureSHA].Date)
	assert.NoError(t, err, "date must be RFC3339")
	assert.False(t, result.Truncated)
}

func TestGitBlame_LineRange(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewGitBlame(mgr, executor, 1024*1024)

	start, end := 3, 4
	result, err := handler.Execute(context.Background(), tools.GitBlameInput{
		Repo: repo, Ref: "main", Path: "src/main.go", StartLine: &start, EndLine: &end,
	})
	require.NoError(t, err)
	require.Len(t, result.Lines, 2)
	assert.Equal(t, 3, result.Lines[0].Line)
	assert.Equal(t, "func main() {", result.Lines[0].Text)
	assert.Equal(t, "\tprintln(\"hello\")", result.Lines[1].Text)
}

func TestGitBlame_Truncates(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	input := tools.GitBlameInput{Repo: repo, Ref: "main", Path: "src/main.go"}

	full, err := tools.NewGitBlame(mgr, executor, 1024*1024).Execute(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, full.Lines, 5)

	// The first record carries the commit header (~300 bytes); later ones are short.
	result, err := tools.NewGitBlame(mgr, executor, 400).Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	require.NotEmpty(t, result.Lines)
	require.Less(t, len(result.Lines), 5)
	assert.Equal(t, full.Lines[:len(result.Lines)], result.Lines)
}

func TestGitBlame_RejectsInvalidInput(t *testing.T) {
	mgr, repo, _ := setupTestRepo(t)
	executor := git.NewExecutor(30 * time.Second)
	handler := tools.NewGitBlame(mgr, executor, 1024*1024)

	zero, one, two := 0, 1, 2
	tests := []tools.GitBlameInput{
		{Repo: repo, Ref: "main"},
		{Repo: repo, Ref: "main", Path: "../x"},
		{Repo: repo, Ref: "--output=/tmp/x", Path: "README.md"},
		{Repo: repo, Ref: "main", Path: "README.md", StartLine: &zero},
		{Repo: repo, Ref: "main", Path: "README.md", StartLine: &two, EndLine: &one},
	}
	for _, in := range tests {
		_, err := handler.Execute(context.Background(), in)
		assert.Error(t, err, "%+v", in)
	}
}
