package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/validate"
)

type GitShowInput struct {
	Repo   string `json:"repo"`
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
	Path   string `json:"path,omitempty"`
}

type GitShowOutput struct {
	SHA       string `json:"sha"`
	Author    string `json:"author"`
	Date      string `json:"date"`
	Message   string `json:"message"`
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

type GitShow struct {
	cache       *cache.Manager
	executor    *git.Executor
	maxDiffSize int
}

func NewGitShow(cache *cache.Manager, executor *git.Executor, maxDiffSize int) *GitShow {
	return &GitShow{cache: cache, executor: executor, maxDiffSize: maxDiffSize}
}

func (h *GitShow) Execute(ctx context.Context, input GitShowInput) (*GitShowOutput, error) {
	if err := validate.Repo(input.Repo); err != nil {
		return nil, err
	}
	if err := validate.Ref(input.Ref); err != nil {
		return nil, err
	}
	if err := validate.Ref(input.Commit); err != nil {
		return nil, err
	}
	if input.Path != "" {
		if err := validate.Path(input.Path); err != nil {
			return nil, err
		}
	}

	sha, err := h.cache.Ensure(ctx, input.Repo, input.Ref, false)
	if err != nil {
		return nil, err
	}

	repoPath := h.cache.RepoPath(input.Repo)

	format := "%H%x00%an <%ae>%x00%aI%x00%B"
	metaOut, metaTruncated, err := h.executor.RunLimited(ctx, repoPath, h.maxDiffSize, "show", "--no-patch", fmt.Sprintf("--format=%s", format), "--end-of-options", input.Commit)
	if err != nil {
		return nil, err
	}

	fields := strings.SplitN(metaOut, "\x00", 4)
	if len(fields) < 4 {
		return nil, fmt.Errorf("unexpected show output format")
	}

	diffArgs := []string{"show", "--first-parent", "--format=", "--end-of-options", input.Commit}
	if input.Path != "" {
		diffArgs = append(diffArgs, "--", input.Path)
	}

	diff, truncated, err := h.executor.RunLimited(ctx, repoPath, h.maxDiffSize, diffArgs...)
	if err != nil {
		return nil, err
	}
	if truncated {
		diff = trimPartialRune(diff)
	}
	message := fields[3]
	if metaTruncated {
		message = trimPartialRune(message)
		truncated = true
	}

	return &GitShowOutput{
		SHA:       sha,
		Author:    fields[1],
		Date:      fields[2],
		Message:   strings.TrimSpace(message),
		Diff:      diff,
		Truncated: truncated,
	}, nil
}

func (i *GitShowInput) GetRepo() string { return i.Repo }
