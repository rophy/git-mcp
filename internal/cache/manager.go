package cache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/toolerr"
)

type UpstreamResolver func(repo string) (url string, token string, err error)

// trashDir holds evicted repos until they are deleted, and cloneDir holds
// clones in progress. Their names have no ".git" suffix, so they never match a
// repo path.
const (
	trashDir = ".trash"
	cloneDir = ".tmp"
)

type repoState struct {
	mu         sync.Mutex // serializes clone and fetch
	refs       int        // requests holding the repo; guarded by Manager.mu
	lastAccess time.Time  // guarded by Manager.mu
	size       int64      // bytes on disk; guarded by Manager.mu
}

type Manager struct {
	cacheDir string
	maxBytes int64 // 0 means no limit
	executor *git.Executor
	resolve  UpstreamResolver

	mu    sync.Mutex
	repos map[string]*repoState
	total int64
}

// NewManager loads the repos already in cacheDir, then evicts repos if they
// exceed maxBytes. maxBytes 0 means no limit.
func NewManager(cacheDir string, maxBytes int64, executor *git.Executor, resolve UpstreamResolver) (*Manager, error) {
	m := &Manager{
		cacheDir: cacheDir,
		maxBytes: maxBytes,
		executor: executor,
		resolve:  resolve,
		repos:    map[string]*repoState{},
	}
	for _, dir := range []string{trashDir, cloneDir} {
		if err := os.RemoveAll(filepath.Join(cacheDir, dir)); err != nil {
			return nil, err
		}
	}
	if err := m.scan(); err != nil {
		return nil, fmt.Errorf("scan cache dir: %w", err)
	}
	m.evict()
	return m, nil
}

// scan records each "<repo>.git" directory in cacheDir. Its mtime is the last access.
func (m *Manager) scan() error {
	err := filepath.WalkDir(m.cacheDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || !strings.HasSuffix(p, ".git") {
			return err
		}
		if !m.repoExists(p) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size, err := dirSize(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(m.cacheDir, p)
		if err != nil {
			return err
		}
		repo := filepath.ToSlash(strings.TrimSuffix(rel, ".git"))
		m.repos[repo] = &repoState{lastAccess: info.ModTime(), size: size}
		m.total += size
		return filepath.SkipDir
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (m *Manager) RepoPath(repo string) string {
	return filepath.Join(m.cacheDir, repo+".git")
}

// acquire marks repo as in use so it is not evicted.
func (m *Manager) acquire(repo string) (*repoState, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.repos[repo]
	if !ok {
		st = &repoState{}
		m.repos[repo] = st
	}
	st.refs++
	st.lastAccess = time.Now()
	return st, sync.OnceFunc(func() {
		m.mu.Lock()
		st.refs--
		m.mu.Unlock()
	})
}

// Ensure makes ref available in the local mirror of repo and returns its SHA.
// The caller must call release when it is done reading the repo; until then
// the repo is not evicted.
func (m *Manager) Ensure(ctx context.Context, repo, ref string, refresh bool) (sha string, release func(), err error) {
	st, release := m.acquire(repo)
	sha, changed, err := m.ensure(ctx, st, repo, ref, refresh)
	if err != nil {
		release()
		return "", nil, err
	}
	if changed {
		m.evict()
	}
	return sha, release, nil
}

// ensure clones or fetches as needed and resolves ref. changed reports whether
// the repo on disk changed.
func (m *Manager) ensure(ctx context.Context, st *repoState, repo, ref string, refresh bool) (sha string, changed bool, err error) {
	repoPath := m.RepoPath(repo)

	st.mu.Lock()
	defer st.mu.Unlock()
	defer func() {
		if changed {
			m.updateSize(st, repoPath)
		}
	}()

	exists := m.repoExists(repoPath)

	if !exists {
		changed = true
		if err := m.cloneRepo(ctx, repo, repoPath); err != nil {
			return "", changed, upstreamErr(fmt.Errorf("clone %s: %w", repo, err))
		}
	}
	now := time.Now()
	_ = os.Chtimes(repoPath, now, now) // persists the access time for scan

	if refresh && exists {
		changed = true
		if err := m.fetchRepo(ctx, repo, repoPath); err != nil {
			return "", changed, upstreamErr(fmt.Errorf("fetch %s: %w", repo, err))
		}
	}

	sha, err = m.resolveRef(ctx, repoPath, ref)
	if err != nil && exists {
		changed = true
		if fetchErr := m.fetchRepo(ctx, repo, repoPath); fetchErr != nil {
			return "", changed, upstreamErr(fmt.Errorf("fetch %s: %w", repo, fetchErr))
		}
		sha, err = m.resolveRef(ctx, repoPath, ref)
	}

	if err != nil {
		code := toolerr.RefNotFound
		if errors.Is(err, git.ErrTimeout) {
			code = toolerr.Timeout
		}
		return "", changed, toolerr.New(code, fmt.Errorf("resolve %s in %s: %w", ref, repo, err))
	}

	return sha, changed, nil
}

// upstreamErr marks a clone or fetch error as a timeout or an upstream error,
// unless it already has a code.
func upstreamErr(err error) error {
	switch {
	case toolerr.CodeOf(err) != toolerr.Internal:
		return err
	case errors.Is(err, git.ErrTimeout):
		return toolerr.New(toolerr.Timeout, err)
	default:
		return toolerr.New(toolerr.UpstreamError, err)
	}
}

func (m *Manager) updateSize(st *repoState, repoPath string) {
	size, err := dirSize(repoPath)
	if err != nil {
		size = 0
	}
	m.mu.Lock()
	m.total += size - st.size
	st.size = size
	m.mu.Unlock()
}

// evict removes least recently used repos that are not in use until the cache
// fits in maxBytes.
func (m *Manager) evict() {
	if m.maxBytes <= 0 {
		return
	}

	m.mu.Lock()
	type candidate struct {
		repo string
		st   *repoState
	}
	var candidates []candidate
	for repo, st := range m.repos {
		if st.refs == 0 && st.size > 0 {
			candidates = append(candidates, candidate{repo, st})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].st.lastAccess.Before(candidates[j].st.lastAccess)
	})

	var trash []string
	for _, c := range candidates {
		if m.total <= m.maxBytes {
			break
		}
		// Rename under the lock, so a new request for the repo clones into a clean path.
		dst, err := m.moveToTrash(m.RepoPath(c.repo))
		if err != nil {
			continue
		}
		m.total -= c.st.size
		delete(m.repos, c.repo)
		trash = append(trash, dst)
	}
	m.mu.Unlock()

	for _, dir := range trash {
		_ = os.RemoveAll(dir)
	}
}

func (m *Manager) moveToTrash(repoPath string) (string, error) {
	dir := filepath.Join(m.cacheDir, trashDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(dir, "repo-")
	if err != nil {
		return "", err
	}
	if err := os.Rename(repoPath, filepath.Join(tmp, "repo")); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

func dirSize(dir string) (int64, error) {
	var size int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
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
		return toolerr.New(toolerr.InvalidInput, err)
	}

	// Clone into a temp dir first, so a failed or killed clone never leaves a
	// broken repo at repoPath.
	tmpRoot := filepath.Join(m.cacheDir, cloneDir)
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(tmpRoot, "clone-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	tmpRepo := filepath.Join(tmp, "repo")
	if _, err := m.executor.RunWithEnv(ctx, ".", authEnv(token), "clone", "--bare", "--end-of-options", url, tmpRepo); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(repoPath), 0o755); err != nil {
		return err
	}
	return os.Rename(tmpRepo, repoPath)
}

func (m *Manager) fetchRepo(ctx context.Context, repo, repoPath string) error {
	_, token, err := m.resolve(repo)
	if err != nil {
		return toolerr.New(toolerr.InvalidInput, err)
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
