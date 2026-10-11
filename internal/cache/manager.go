package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rophy/git-mcp/internal/git"
)

type UpstreamResolver func(repo string) (url string, token string, err error)

type Manager struct {
	cacheDir string
	executor *git.Executor
	resolve  UpstreamResolver
	mu       sync.Map // per-repo mutexes
}

func NewManager(cacheDir string, executor *git.Executor, resolve UpstreamResolver) *Manager {
	return &Manager{
		cacheDir: cacheDir,
		executor: executor,
		resolve:  resolve,
	}
}

func (m *Manager) repoMutex(repo string) *sync.Mutex {
	v, _ := m.mu.LoadOrStore(repo, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (m *Manager) RepoPath(repo string) string {
	return filepath.Join(m.cacheDir, repo+".git")
}

func (m *Manager) Ensure(ctx context.Context, repo, ref string, refresh bool) (string, error) {
	repoPath := m.RepoPath(repo)

	mu := m.repoMutex(repo)
	mu.Lock()
	defer mu.Unlock()

	exists := m.repoExists(repoPath)

	if !exists {
		if err := m.cloneRepo(ctx, repo, repoPath); err != nil {
			return "", fmt.Errorf("clone %s: %w", repo, err)
		}
	}

	if refresh && exists {
		if err := m.fetchRepo(ctx, repo, repoPath); err != nil {
			return "", fmt.Errorf("fetch %s: %w", repo, err)
		}
	}

	sha, err := m.resolveRef(ctx, repoPath, ref)
	if err != nil && exists {
		if fetchErr := m.fetchRepo(ctx, repo, repoPath); fetchErr != nil {
			return "", fmt.Errorf("fetch %s: %w", repo, fetchErr)
		}
		sha, err = m.resolveRef(ctx, repoPath, ref)
	}

	if err != nil {
		return "", fmt.Errorf("resolve %s in %s: %w", ref, repo, err)
	}

	return sha, nil
}

func (m *Manager) repoExists(repoPath string) bool {
	_, err := os.Stat(filepath.Join(repoPath, "HEAD"))
	return err == nil
}

func (m *Manager) resolveRef(ctx context.Context, repoPath, ref string) (string, error) {
	out, err := m.executor.Run(ctx, repoPath, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (m *Manager) cloneRepo(ctx context.Context, repo, repoPath string) error {
	url, token, err := m.resolve(repo)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(repoPath), 0o755); err != nil {
		return err
	}

	_, err = m.executor.RunWithEnv(ctx, ".", authEnv(token), "clone", "--bare", "--end-of-options", url, repoPath)
	return err
}

func (m *Manager) fetchRepo(ctx context.Context, repo, repoPath string) error {
	_, token, err := m.resolve(repo)
	if err != nil {
		return err
	}

	_, err = m.executor.RunWithEnv(ctx, repoPath, authEnv(token), "fetch", "--prune", "--end-of-options", "origin", "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*")
	return err
}

func authEnv(token string) []string {
	if token == "" {
		return nil
	}
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Bearer " + token,
	}
}
