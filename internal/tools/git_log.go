package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/validate"
)

type GitLogInput struct {
	Repo  string `json:"repo"`
	Ref   string `json:"ref"`
	Count int    `json:"count,omitempty"`
	Path  string `json:"path,omitempty"`
}

type LogEntry struct {
	SHA     string `json:"sha"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Message string `json:"message"`
}

type GitLogOutput struct {
	SHA       string     `json:"sha"`
	Commits   []LogEntry `json:"commits"`
	Truncated bool       `json:"truncated"`
}

type GitLog struct {
	cache         *cache.Manager
	executor      *git.Executor
	maxOutputSize int
}

func NewGitLog(cache *cache.Manager, executor *git.Executor, maxOutputSize int) *GitLog {
	return &GitLog{cache: cache, executor: executor, maxOutputSize: maxOutputSize}
}

func (h *GitLog) Execute(ctx context.Context, input GitLogInput) (*GitLogOutput, error) {
	if err := validate.Repo(input.Repo); err != nil {
		return nil, err
	}
	if err := validate.Ref(input.Ref); err != nil {
		return nil, err
	}
	if input.Path != "" {
		if err := validate.Path(input.Path); err != nil {
			return nil, err
		}
	}

	count := input.Count
	if count <= 0 {
		count = 20
	}
	if count > 100 {
		count = 100
	}

	sha, release, err := h.cache.Ensure(ctx, input.Repo, input.Ref, false)
	if err != nil {
		return nil, err
	}
	defer release()

	repoPath := h.cache.RepoPath(input.Repo)
	format := "%H%x00%an <%ae>%x00%aI%x00%s%x00"
	args := []string{"log", "--first-parent", fmt.Sprintf("--format=%s", format), fmt.Sprintf("-n%d", count), "--end-of-options", sha}
	if input.Path != "" {
		args = append(args, "--", input.Path)
	}

	out, truncated, err := h.executor.RunLimited(ctx, repoPath, h.maxOutputSize, args...)
	if err != nil {
		return nil, err
	}

	commits := []LogEntry{}
	for _, record := range strings.Split(completeRecords(out, truncated, '\n'), "\n") {
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x00", 5)
		if len(fields) < 4 {
			continue
		}
		commits = append(commits, LogEntry{
			SHA:     fields[0],
			Author:  fields[1],
			Date:    fields[2],
			Message: fields[3],
		})
	}

	return &GitLogOutput{SHA: sha, Commits: commits, Truncated: truncated}, nil
}

func (i *GitLogInput) GetRepo() string { return i.Repo }
