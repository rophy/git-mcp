package tools

import (
	"context"
	"strings"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/validate"
)

type ListFilesInput struct {
	Repo      string `json:"repo"`
	Ref       string `json:"ref"`
	Path      string `json:"path,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
}

type FileEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file" or "directory"
}

type ListFilesOutput struct {
	SHA     string      `json:"sha"`
	Entries []FileEntry `json:"entries"`
}

type ListFiles struct {
	cache    *cache.Manager
	executor *git.Executor
}

func NewListFiles(cache *cache.Manager, executor *git.Executor) *ListFiles {
	return &ListFiles{cache: cache, executor: executor}
}

func (h *ListFiles) Execute(ctx context.Context, input ListFilesInput) (*ListFilesOutput, error) {
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

	sha, err := h.cache.Ensure(ctx, input.Repo, input.Ref, false)
	if err != nil {
		return nil, err
	}

	repoPath := h.cache.RepoPath(input.Repo)
	args := []string{"ls-tree"}
	if input.Recursive {
		args = append(args, "-r")
	}
	target := sha
	if input.Path != "" {
		target = sha + ":" + input.Path
	}
	args = append(args, "--end-of-options", target)

	out, err := h.executor.Run(ctx, repoPath, args...)
	if err != nil {
		return nil, err
	}

	entries := []FileEntry{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		// Format: <mode> <type> <hash>\t<name>
		tabIdx := strings.IndexByte(line, '\t')
		if tabIdx < 0 {
			continue
		}
		parts := strings.Fields(line[:tabIdx])
		if len(parts) < 3 {
			continue
		}
		entryType := "file"
		if parts[1] == "tree" {
			entryType = "directory"
		}
		entries = append(entries, FileEntry{Name: line[tabIdx+1:], Type: entryType})
	}

	return &ListFilesOutput{SHA: sha, Entries: entries}, nil
}
