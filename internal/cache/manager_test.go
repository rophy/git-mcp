package cache_test

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/toolerr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupUpstreamRepo(t *testing.T) (upstreamPath string, expectedSHA string) {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "upstream")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	env := []string{
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	}

	run := func(args ...string) string {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "cmd %v: %s", args, out)
		return string(out)
	}

	run("git", "init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello\n"), 0o644))
	run("git", "add", ".")
	run("git", "commit", "-m", "initial")
	sha := run("git", "rev-parse", "HEAD")

	bareDir := filepath.Join(dir, "upstream.git")
	cmd := exec.Command("git", "clone", "--bare", workDir, bareDir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "bare clone: %s", out)

	return bareDir, sha[:40]
}

func newManager(t *testing.T, cacheDir, upstreamPath string, maxBytes int64) *cache.Manager {
	t.Helper()
	executor := git.NewExecutor(30 * time.Second)
	mgr, err := cache.NewManager(cacheDir, maxBytes, executor, func(repo string) (string, string, string, error) {
		return upstreamPath, "", "", nil
	})
	require.NoError(t, err)
	return mgr
}

// ensure runs Ensure and releases the repo right away.
func ensure(t *testing.T, mgr *cache.Manager, repo string) {
	t.Helper()
	_, release, err := mgr.Ensure(context.Background(), repo, "main", false)
	require.NoError(t, err)
	release()
}

func dirSize(t *testing.T, dir string) int64 {
	t.Helper()
	var size int64
	require.NoError(t, filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	}))
	return size
}

// repoSize returns the on-disk size of one cached clone of upstreamPath.
func repoSize(t *testing.T, upstreamPath string) int64 {
	t.Helper()
	cacheDir := t.TempDir()
	mgr := newManager(t, cacheDir, upstreamPath, 0)
	ensure(t, mgr, "probe/org/repo")
	return dirSize(t, mgr.RepoPath("probe/org/repo"))
}

func cached(mgr *cache.Manager, repo string) bool {
	_, err := os.Stat(filepath.Join(mgr.RepoPath(repo), "HEAD"))
	return err == nil
}

func TestEnsure_CloneAndResolve(t *testing.T) {
	upstreamPath, expectedSHA := setupUpstreamRepo(t)
	mgr := newManager(t, t.TempDir(), upstreamPath, 0)

	sha, release, err := mgr.Ensure(context.Background(), "local/test/repo", "main", false)
	require.NoError(t, err)
	defer release()
	assert.Equal(t, expectedSHA, sha)
}

func TestEnsure_CachedResolve(t *testing.T) {
	upstreamPath, expectedSHA := setupUpstreamRepo(t)
	mgr := newManager(t, t.TempDir(), upstreamPath, 0)

	sha1, release1, err := mgr.Ensure(context.Background(), "local/test/repo", "main", false)
	require.NoError(t, err)
	release1()

	sha2, release2, err := mgr.Ensure(context.Background(), "local/test/repo", sha1, false)
	require.NoError(t, err)
	release2()
	assert.Equal(t, expectedSHA, sha2)
}

func TestEnsure_RepoPath(t *testing.T) {
	upstreamPath, _ := setupUpstreamRepo(t)
	cacheDir := t.TempDir()
	mgr := newManager(t, cacheDir, upstreamPath, 0)

	ensure(t, mgr, "github.com/org/repo")

	expectedPath := filepath.Join(cacheDir, "github.com", "org", "repo.git")
	_, err := os.Stat(expectedPath)
	assert.NoError(t, err, "bare repo should exist at %s", expectedPath)
}

func TestEviction_UnlimitedKeepsAll(t *testing.T) {
	upstreamPath, _ := setupUpstreamRepo(t)
	mgr := newManager(t, t.TempDir(), upstreamPath, 0)

	for _, repo := range []string{"h/o/a", "h/o/b", "h/o/c"} {
		ensure(t, mgr, repo)
	}
	assert.True(t, cached(mgr, "h/o/a"))
	assert.True(t, cached(mgr, "h/o/b"))
	assert.True(t, cached(mgr, "h/o/c"))
}

func TestEviction_EvictsLeastRecentlyUsed(t *testing.T) {
	upstreamPath, _ := setupUpstreamRepo(t)
	size := repoSize(t, upstreamPath)
	mgr := newManager(t, t.TempDir(), upstreamPath, size*5/2)

	ensure(t, mgr, "h/o/a")
	ensure(t, mgr, "h/o/b")
	ensure(t, mgr, "h/o/a") // a is now more recent than b
	ensure(t, mgr, "h/o/c") // 3 repos > 2.5 sizes: evict b

	assert.True(t, cached(mgr, "h/o/a"))
	assert.False(t, cached(mgr, "h/o/b"))
	assert.True(t, cached(mgr, "h/o/c"))
}

func TestEviction_NeverEvictsRepoInUse(t *testing.T) {
	upstreamPath, _ := setupUpstreamRepo(t)
	size := repoSize(t, upstreamPath)
	mgr := newManager(t, t.TempDir(), upstreamPath, size*3/2)

	_, releaseA, err := mgr.Ensure(context.Background(), "h/o/a", "main", false)
	require.NoError(t, err)
	ensure(t, mgr, "h/o/b") // over the limit, but a is in use and b was just used

	assert.True(t, cached(mgr, "h/o/a"), "a is in use")
	assert.True(t, cached(mgr, "h/o/b"), "b is the newest repo")

	releaseA()
	releaseA() // release is idempotent
	ensure(t, mgr, "h/o/c")

	assert.False(t, cached(mgr, "h/o/a"))
	assert.False(t, cached(mgr, "h/o/b"))
	assert.True(t, cached(mgr, "h/o/c"))
}

func TestEviction_ReclonesEvictedRepo(t *testing.T) {
	upstreamPath, expectedSHA := setupUpstreamRepo(t)
	size := repoSize(t, upstreamPath)
	mgr := newManager(t, t.TempDir(), upstreamPath, size*3/2)

	ensure(t, mgr, "h/o/a")
	ensure(t, mgr, "h/o/b")
	require.False(t, cached(mgr, "h/o/a"))

	sha, release, err := mgr.Ensure(context.Background(), "h/o/a", "main", false)
	require.NoError(t, err)
	defer release()
	assert.Equal(t, expectedSHA, sha)
	assert.True(t, cached(mgr, "h/o/a"))
}

func TestEviction_UsesAccessOrderFromDiskAfterRestart(t *testing.T) {
	upstreamPath, _ := setupUpstreamRepo(t)
	size := repoSize(t, upstreamPath)
	cacheDir := t.TempDir()

	first := newManager(t, cacheDir, upstreamPath, 0)
	ensure(t, first, "h/o/a")
	ensure(t, first, "h/o/b")
	ensure(t, first, "h/o/a") // no clone; only the access time changes

	restarted := newManager(t, cacheDir, upstreamPath, size*5/2)
	ensure(t, restarted, "h/o/c") // 3 repos > 2.5 sizes: evict b, the oldest

	assert.True(t, cached(restarted, "h/o/a"))
	assert.False(t, cached(restarted, "h/o/b"))
	assert.True(t, cached(restarted, "h/o/c"))
}

func TestEviction_EvictsAtStartupWhenOverLimit(t *testing.T) {
	upstreamPath, _ := setupUpstreamRepo(t)
	size := repoSize(t, upstreamPath)
	cacheDir := t.TempDir()

	first := newManager(t, cacheDir, upstreamPath, 0)
	ensure(t, first, "h/o/a")
	ensure(t, first, "h/o/b")

	restarted := newManager(t, cacheDir, upstreamPath, size*3/2)
	assert.False(t, cached(restarted, "h/o/a"))
	assert.True(t, cached(restarted, "h/o/b"))
	_, err := os.Stat(filepath.Join(cacheDir, ".trash"))
	assert.True(t, os.IsNotExist(err) || dirSize(t, filepath.Join(cacheDir, ".trash")) == 0)
}

func TestEviction_ConcurrentUse(t *testing.T) {
	upstreamPath, expectedSHA := setupUpstreamRepo(t)
	size := repoSize(t, upstreamPath)
	mgr := newManager(t, t.TempDir(), upstreamPath, size*3/2)
	executor := git.NewExecutor(30 * time.Second)

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			repo := fmt.Sprintf("h/o/r%d", i%4)
			sha, release, err := mgr.Ensure(context.Background(), repo, "main", false)
			if err != nil {
				errs <- err
				return
			}
			defer release()
			// While the repo is held, it must stay readable.
			out, err := executor.Run(context.Background(), mgr.RepoPath(repo), "cat-file", "-t", sha)
			if err != nil || out != "commit" {
				errs <- fmt.Errorf("read %s: %q %v", repo, out, err)
			}
			if sha != expectedSHA {
				errs <- fmt.Errorf("sha %s", sha)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestEnsure_KilledCloneLeavesNoBrokenRepo(t *testing.T) {
	upstreamPath, expectedSHA := setupUpstreamRepo(t)
	out, err := exec.Command("git", "-C", upstreamPath, "update-server-info").CombinedOutput()
	require.NoError(t, err, "%s", out)

	// Dumb HTTP upstream that serves refs but never sends objects.
	files := http.FileServer(http.Dir(upstreamPath))
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/objects/") {
			<-r.Context().Done()
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer stalled.Close()

	cacheDir := t.TempDir()
	upstreamURL := stalled.URL
	mgr, err := cache.NewManager(cacheDir, 0, git.NewExecutor(time.Second), func(string) (string, string, string, error) {
		return upstreamURL, "", "", nil
	})
	require.NoError(t, err)

	_, _, err = mgr.Ensure(context.Background(), "h/o/a", "main", false)
	require.Error(t, err, "clone must time out")
	assert.Equal(t, toolerr.Timeout, toolerr.CodeOf(err), "%v", err)
	_, err = os.Stat(mgr.RepoPath("h/o/a"))
	assert.True(t, os.IsNotExist(err), "killed clone left a directory at the repo path")

	// Once the upstream works, the repo must clone normally.
	upstreamURL = upstreamPath
	sha, release, err := mgr.Ensure(context.Background(), "h/o/a", "main", false)
	require.NoError(t, err)
	defer release()
	assert.Equal(t, expectedSHA, sha)
}

func TestEnsure_ErrorCodes(t *testing.T) {
	upstreamPath, _ := setupUpstreamRepo(t)

	t.Run("ref not found after successful fetch", func(t *testing.T) {
		mgr := newManager(t, t.TempDir(), upstreamPath, 0)
		ensure(t, mgr, "h/o/a")
		_, _, err := mgr.Ensure(context.Background(), "h/o/a", "no-such-branch", false)
		assert.Equal(t, toolerr.RefNotFound, toolerr.CodeOf(err), "%v", err)
	})

	t.Run("ref not found on first clone", func(t *testing.T) {
		mgr := newManager(t, t.TempDir(), upstreamPath, 0)
		_, _, err := mgr.Ensure(context.Background(), "h/o/a", "no-such-branch", false)
		assert.Equal(t, toolerr.RefNotFound, toolerr.CodeOf(err), "%v", err)
	})

	t.Run("clone fails", func(t *testing.T) {
		mgr := newManager(t, t.TempDir(), filepath.Join(t.TempDir(), "missing.git"), 0)
		_, _, err := mgr.Ensure(context.Background(), "h/o/a", "main", false)
		assert.Equal(t, toolerr.UpstreamError, toolerr.CodeOf(err), "%v", err)
	})

	t.Run("fetch fails", func(t *testing.T) {
		upstream := upstreamPath
		executor := git.NewExecutor(30 * time.Second)
		mgr, err := cache.NewManager(t.TempDir(), 0, executor, func(string) (string, string, string, error) {
			return upstream, "", "", nil
		})
		require.NoError(t, err)
		ensure(t, mgr, "h/o/a")
		require.NoError(t, os.RemoveAll(upstreamPath+".moved"))
		require.NoError(t, os.Rename(upstreamPath, upstreamPath+".moved"))
		t.Cleanup(func() { os.Rename(upstreamPath+".moved", upstreamPath) })

		_, _, err = mgr.Ensure(context.Background(), "h/o/a", "main", true)
		assert.Equal(t, toolerr.UpstreamError, toolerr.CodeOf(err), "%v", err)
	})

	t.Run("no upstream configured", func(t *testing.T) {
		executor := git.NewExecutor(30 * time.Second)
		mgr, err := cache.NewManager(t.TempDir(), 0, executor, func(string) (string, string, string, error) {
			return "", "", "", fmt.Errorf("no upstream configured for host %q", "h")
		})
		require.NoError(t, err)
		_, _, err = mgr.Ensure(context.Background(), "h/o/a", "main", false)
		assert.Equal(t, toolerr.InvalidInput, toolerr.CodeOf(err), "%v", err)
	})
}

// basicAuthUpstream serves upstreamPath over dumb HTTP and requires Basic auth.
// It reports whether any request came without credentials.
func basicAuthUpstream(t *testing.T, upstreamPath, user, pass string) (url string, sawAnonymous *atomic.Bool) {
	t.Helper()
	out, err := exec.Command("git", "-C", upstreamPath, "update-server-info").CombinedOutput()
	require.NoError(t, err, "%s", out)
	files := http.FileServer(http.Dir(upstreamPath))
	sawAnonymous = &atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok {
			sawAnonymous.Store(true)
		}
		if !ok || u != user || p != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, sawAnonymous
}

func TestEnsure_UpstreamCredentials(t *testing.T) {
	upstreamPath, expectedSHA := setupUpstreamRepo(t)
	upstreamURL, sawAnonymous := basicAuthUpstream(t, upstreamPath, "bot", "s3cret-value")
	executor := git.NewExecutor(30 * time.Second)
	newMgr := func(user, pass string) *cache.Manager {
		mgr, err := cache.NewManager(t.TempDir(), 0, executor, func(string) (string, string, string, error) {
			return upstreamURL, user, pass, nil
		})
		require.NoError(t, err)
		return mgr
	}

	mgr := newMgr("bot", "s3cret-value")
	sha, release, err := mgr.Ensure(context.Background(), "h/o/a", "main", false)
	require.NoError(t, err)
	release()
	assert.Equal(t, expectedSHA, sha)
	assert.True(t, sawAnonymous.Load(), "git must send credentials only after the server asks")

	_, release, err = mgr.Ensure(context.Background(), "h/o/a", "main", true)
	require.NoError(t, err, "fetch with credentials")
	release()

	// No file in the cached repo may contain the password.
	require.NoError(t, filepath.WalkDir(mgr.RepoPath("h/o/a"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "s3cret-value", p)
		return nil
	}))

	for name, creds := range map[string][2]string{"wrong password": {"bot", "nope"}, "no credentials": {"", ""}} {
		_, _, err := newMgr(creds[0], creds[1]).Ensure(context.Background(), "h/o/a", "main", false)
		assert.Equal(t, toolerr.UpstreamError, toolerr.CodeOf(err), "%s: %v", name, err)
	}
}
