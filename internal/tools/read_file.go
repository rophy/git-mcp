package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/toolerr"
	"github.com/rophy/git-mcp/internal/validate"
)

type ReadFileInput struct {
	Repo      string `json:"repo"`
	Ref       string `json:"ref"`
	Path      string `json:"path"`
	StartLine *int   `json:"start_line,omitempty"`
	EndLine   *int   `json:"end_line,omitempty"`
}

type ReadFileOutput struct {
	SHA     string `json:"sha"`
	Content string `json:"content"`
}

type ReadFile struct {
	cache       *cache.Manager
	executor    *git.Executor
	maxFileSize int
}

func NewReadFile(cache *cache.Manager, executor *git.Executor, maxFileSize int) *ReadFile {
	return &ReadFile{cache: cache, executor: executor, maxFileSize: maxFileSize}
}

func (h *ReadFile) Execute(ctx context.Context, input ReadFileInput) (*ReadFileOutput, error) {
	if err := validate.Repo(input.Repo); err != nil {
		return nil, err
	}
	if err := validate.Ref(input.Ref); err != nil {
		return nil, err
	}
	if input.Path == "" {
		return nil, toolerr.Errorf(toolerr.InvalidInput, "path is required")
	}
	if err := validate.Path(input.Path); err != nil {
		return nil, err
	}

	sha, release, err := h.cache.Ensure(ctx, input.Repo, input.Ref, false)
	if err != nil {
		return nil, err
	}
	defer release()

	repoPath := h.cache.RepoPath(input.Repo)
	content, truncated, err := h.executor.RunLimited(ctx, repoPath, h.maxFileSize, "cat-file", "blob", fmt.Sprintf("%s:%s", sha, input.Path))
	if err != nil {
		return nil, err
	}

	if truncated {
		return nil, toolerr.Errorf(toolerr.InvalidInput, "file exceeds max size (%d bytes)", h.maxFileSize)
	}

	// A final newline ends the last line; it does not start a new one.
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	start := 1
	end := len(lines)
	if input.StartLine != nil {
		start = *input.StartLine
	}
	if input.EndLine != nil {
		end = *input.EndLine
	}
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}

	var numbered strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&numbered, "%d\t%s\n", i, lines[i-1])
	}

	return &ReadFileOutput{SHA: sha, Content: numbered.String()}, nil
}
