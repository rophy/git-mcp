package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/validate"
)

type GitBlameInput struct {
	Repo      string `json:"repo"`
	Ref       string `json:"ref"`
	Path      string `json:"path"`
	StartLine *int   `json:"start_line,omitempty"`
	EndLine   *int   `json:"end_line,omitempty"`
}

type BlameLine struct {
	Line   int    `json:"line"`
	Commit string `json:"commit"`
	Text   string `json:"text"`
}

type BlameCommit struct {
	Author  string `json:"author"`
	Date    string `json:"date"`
	Summary string `json:"summary"`
}

type GitBlameOutput struct {
	SHA       string                 `json:"sha"`
	Lines     []BlameLine            `json:"lines"`
	Commits   map[string]BlameCommit `json:"commits"`
	Truncated bool                   `json:"truncated"`
}

type GitBlame struct {
	cache         *cache.Manager
	executor      *git.Executor
	maxOutputSize int
}

func NewGitBlame(cache *cache.Manager, executor *git.Executor, maxOutputSize int) *GitBlame {
	return &GitBlame{cache: cache, executor: executor, maxOutputSize: maxOutputSize}
}

func (h *GitBlame) Execute(ctx context.Context, input GitBlameInput) (*GitBlameOutput, error) {
	if err := validate.Repo(input.Repo); err != nil {
		return nil, err
	}
	if err := validate.Ref(input.Ref); err != nil {
		return nil, err
	}
	if input.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if err := validate.Path(input.Path); err != nil {
		return nil, err
	}
	lineRange, err := blameRange(input.StartLine, input.EndLine)
	if err != nil {
		return nil, err
	}

	sha, release, err := h.cache.Ensure(ctx, input.Repo, input.Ref, false)
	if err != nil {
		return nil, err
	}
	defer release()

	args := []string{"blame", "--porcelain"}
	if lineRange != "" {
		args = append(args, "-L", lineRange)
	}
	// git blame rejects --end-of-options; sha is a resolved hex SHA, so it
	// cannot be read as an option.
	args = append(args, sha, "--", input.Path)

	repoPath := h.cache.RepoPath(input.Repo)
	out, truncated, err := h.executor.RunLimited(ctx, repoPath, h.maxOutputSize, args...)
	if err != nil {
		return nil, err
	}

	lines, commits := parseBlame(completeRecords(out, truncated, '\n'))
	return &GitBlameOutput{SHA: sha, Lines: lines, Commits: commits, Truncated: truncated}, nil
}

// blameRange returns the -L value for the optional 1-based line range.
func blameRange(start, end *int) (string, error) {
	if start == nil && end == nil {
		return "", nil
	}
	s, e := 1, 0
	if start != nil {
		s = *start
	}
	if end != nil {
		e = *end
	}
	if s < 1 || (end != nil && e < s) {
		return "", fmt.Errorf("invalid line range: start_line=%d end_line=%d", s, e)
	}
	if end == nil {
		return fmt.Sprintf("%d,", s), nil
	}
	return fmt.Sprintf("%d,%d", s, e), nil
}

// parseBlame parses "git blame --porcelain" output. Each line has a header
// "<sha> <orig-line> <final-line> [<count>]", commit fields the first time the
// commit appears, and the content line prefixed with a tab. Only commits that
// own a returned line are kept.
func parseBlame(out string) ([]BlameLine, map[string]BlameCommit) {
	lines := []BlameLine{}
	seen := map[string]BlameCommit{}
	commits := map[string]BlameCommit{}

	var sha string
	var final int
	var unix int64
	for _, line := range strings.Split(out, "\n") {
		if text, ok := strings.CutPrefix(line, "\t"); ok {
			if sha == "" {
				continue
			}
			lines = append(lines, BlameLine{Line: final, Commit: sha, Text: text})
			commits[sha] = seen[sha]
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		c := seen[sha]
		switch key {
		case "author":
			c.Author = value
		case "author-mail":
			c.Author = strings.TrimSpace(c.Author + " " + value)
		case "author-time":
			unix, _ = strconv.ParseInt(value, 10, 64)
		case "author-tz":
			c.Date = blameDate(unix, value)
		case "summary":
			c.Summary = value
		default:
			if fields := strings.Fields(line); len(fields) >= 3 && len(fields[0]) == 40 {
				if n, err := strconv.Atoi(fields[2]); err == nil {
					sha, final = fields[0], n
				}
			}
			continue
		}
		seen[sha] = c
	}
	return lines, commits
}

// blameDate formats a unix time and a "+hhmm" zone as RFC3339.
func blameDate(unix int64, tz string) string {
	t := time.Unix(unix, 0).UTC()
	if len(tz) == 5 {
		h, errH := strconv.Atoi(tz[1:3])
		m, errM := strconv.Atoi(tz[3:5])
		if errH == nil && errM == nil {
			offset := h*3600 + m*60
			if tz[0] == '-' {
				offset = -offset
			}
			t = t.In(time.FixedZone(tz, offset))
		}
	}
	return t.Format(time.RFC3339)
}
