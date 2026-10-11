package tools

import (
	"context"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/validate"
)

type GitDiffInput struct {
	Repo      string `json:"repo"`
	Base      string `json:"base"`
	Head      string `json:"head"`
	Path      string `json:"path,omitempty"`
	MergeBase bool   `json:"merge_base,omitempty"`
}

type GitDiffOutput struct {
	BaseSHA   string `json:"base_sha"`
	HeadSHA   string `json:"head_sha"`
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

type GitDiff struct {
	cache       *cache.Manager
	executor    *git.Executor
	maxDiffSize int
}

func NewGitDiff(cache *cache.Manager, executor *git.Executor, maxDiffSize int) *GitDiff {
	return &GitDiff{cache: cache, executor: executor, maxDiffSize: maxDiffSize}
}

func (h *GitDiff) Execute(ctx context.Context, input GitDiffInput) (*GitDiffOutput, error) {
	if err := validate.Repo(input.Repo); err != nil {
		return nil, err
	}
	if err := validate.Ref(input.Base); err != nil {
		return nil, err
	}
	if err := validate.Ref(input.Head); err != nil {
		return nil, err
	}
	if input.Path != "" {
		if err := validate.Path(input.Path); err != nil {
			return nil, err
		}
	}

	baseSHA, releaseBase, err := h.cache.Ensure(ctx, input.Repo, input.Base, false)
	if err != nil {
		return nil, err
	}
	defer releaseBase()
	headSHA, releaseHead, err := h.cache.Ensure(ctx, input.Repo, input.Head, false)
	if err != nil {
		return nil, err
	}
	defer releaseHead()

	// Both sides are resolved SHAs, so "..." cannot be confused with ref syntax.
	args := []string{"diff", "--end-of-options", baseSHA, headSHA}
	if input.MergeBase {
		args = []string{"diff", "--end-of-options", baseSHA + "..." + headSHA}
	}
	if input.Path != "" {
		args = append(args, "--", input.Path)
	}

	repoPath := h.cache.RepoPath(input.Repo)
	diff, truncated, err := h.executor.RunLimited(ctx, repoPath, h.maxDiffSize, args...)
	if err != nil {
		return nil, err
	}
	if truncated {
		diff = trimPartialRune(diff)
	}

	return &GitDiffOutput{BaseSHA: baseSHA, HeadSHA: headSHA, Diff: diff, Truncated: truncated}, nil
}

func (i *GitDiffInput) GetRepo() string { return i.Repo }
