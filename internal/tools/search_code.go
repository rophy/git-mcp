package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/validate"
)

type SearchCodeInput struct {
	Repo       string `json:"repo"`
	Ref        string `json:"ref"`
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	MaxMatches int    `json:"max_matches,omitempty"`
}

type SearchMatch struct {
	Path       string `json:"path"`
	LineNumber int    `json:"line_number"`
	LineText   string `json:"line_text"`
}

type SearchCodeOutput struct {
	SHA       string        `json:"sha"`
	Matches   []SearchMatch `json:"matches"`
	Truncated bool          `json:"truncated"`
}

type SearchCode struct {
	cache         *cache.Manager
	executor      *git.Executor
	maxMatches    int
	maxOutputSize int
}

func NewSearchCode(cache *cache.Manager, executor *git.Executor, maxMatches, maxOutputSize int) *SearchCode {
	return &SearchCode{cache: cache, executor: executor, maxMatches: maxMatches, maxOutputSize: maxOutputSize}
}

func (h *SearchCode) Execute(ctx context.Context, input SearchCodeInput) (*SearchCodeOutput, error) {
	if err := validate.Repo(input.Repo); err != nil {
		return nil, err
	}
	if err := validate.Ref(input.Ref); err != nil {
		return nil, err
	}
	if err := validate.Pattern(input.Pattern); err != nil {
		return nil, err
	}
	if input.Path != "" {
		if err := validate.Path(input.Path); err != nil {
			return nil, err
		}
	}

	sha, release, err := h.cache.Ensure(ctx, input.Repo, input.Ref, false)
	if err != nil {
		return nil, err
	}
	defer release()

	limit := h.maxMatches
	if input.MaxMatches > 0 && input.MaxMatches < limit {
		limit = input.MaxMatches
	}

	repoPath := h.cache.RepoPath(input.Repo)
	args := []string{"grep", "-n", "-E", fmt.Sprintf("-m%d", limit+1), "-e", input.Pattern, sha}
	if input.Path != "" {
		args = append(args, "--", input.Path)
	}

	out, outTruncated, err := h.executor.RunLimited(ctx, repoPath, h.maxOutputSize, args...)
	if err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			return &SearchCodeOutput{SHA: sha, Matches: []SearchMatch{}}, nil
		}
		return nil, err
	}

	lines := strings.Split(completeLines(out, outTruncated), "\n")
	truncated := outTruncated
	if len(lines) > limit {
		lines = lines[:limit]
		truncated = true
	}

	matches := make([]SearchMatch, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		after := strings.TrimPrefix(line, sha+":")
		parts := strings.SplitN(after, ":", 3)
		if len(parts) < 3 {
			continue
		}
		lineNum, _ := strconv.Atoi(parts[1])
		matches = append(matches, SearchMatch{
			Path:       parts[0],
			LineNumber: lineNum,
			LineText:   parts[2],
		})
	}

	return &SearchCodeOutput{SHA: sha, Matches: matches, Truncated: truncated}, nil
}

func (i *SearchCodeInput) GetRepo() string { return i.Repo }
