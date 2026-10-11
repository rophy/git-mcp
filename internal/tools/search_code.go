package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
	args := []string{"grep", "-z", "-n", "-E", fmt.Sprintf("-m%d", limit+1), "-e", input.Pattern, sha}
	if input.Path != "" {
		args = append(args, "--", input.Path)
	}

	out, outTruncated, err := h.executor.RunLimited(ctx, repoPath, h.maxOutputSize, args...)
	if err != nil {
		// git grep exits 1 when nothing matches.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return &SearchCodeOutput{SHA: sha, Matches: []SearchMatch{}}, nil
		}
		return nil, err
	}

	matches := parseGrep(out, sha+":", outTruncated)
	truncated := outTruncated
	if len(matches) > limit {
		matches = matches[:limit]
		truncated = true
	}

	return &SearchCodeOutput{SHA: sha, Matches: matches, Truncated: truncated}, nil
}

// parseGrep parses "git grep -z -n" output: "<rev>:<path>\0<line>\0<text>\n".
// With -z, paths are not quoted and may contain ':'. When truncated, the
// partial last record is dropped.
func parseGrep(out, revPrefix string, truncated bool) []SearchMatch {
	matches := []SearchMatch{}
	for out != "" {
		path, rest, ok := strings.Cut(out, "\x00")
		if !ok {
			break
		}
		num, rest, ok := strings.Cut(rest, "\x00")
		if !ok {
			break
		}
		text, rest, ok := strings.Cut(rest, "\n")
		if !ok && truncated {
			break
		}
		out = rest
		lineNum, err := strconv.Atoi(num)
		if err != nil {
			continue
		}
		matches = append(matches, SearchMatch{
			Path:       strings.TrimPrefix(path, revPrefix),
			LineNumber: lineNum,
			LineText:   text,
		})
	}
	return matches
}
