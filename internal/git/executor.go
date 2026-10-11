package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Executor struct {
	timeout time.Duration
}

func NewExecutor(timeout time.Duration) *Executor {
	return &Executor{timeout: timeout}
}

func (e *Executor) Run(ctx context.Context, repoPath string, args ...string) (string, error) {
	return e.RunWithEnv(ctx, repoPath, nil, args...)
}

func (e *Executor) RunWithEnv(ctx context.Context, repoPath string, extraEnv []string, args ...string) (string, error) {
	out, _, err := e.run(ctx, repoPath, extraEnv, 0, args...)
	return out, err
}

// RunLimited runs git and keeps at most maxBytes of stdout. When the output is
// larger, git is stopped and the first maxBytes are returned with truncated=true.
func (e *Executor) RunLimited(ctx context.Context, repoPath string, maxBytes int, args ...string) (string, bool, error) {
	return e.run(ctx, repoPath, nil, maxBytes, args...)
}

func (e *Executor) run(ctx context.Context, repoPath string, extraEnv []string, maxBytes int, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	runCtx, stop := context.WithCancel(ctx)
	defer stop()

	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = repoPath
	cmd.Env = append(sanitizeEnv(), extraEnv...)

	stdout := &limitWriter{limit: maxBytes, stop: stop}
	var stderr bytes.Buffer
	cmd.Stdout = stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil && !stdout.exceeded {
		if ctx.Err() == context.DeadlineExceeded {
			return "", false, fmt.Errorf("git command timed out after %s", e.timeout)
		}
		return "", false, fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), stderr.String(), err)
	}

	if stdout.exceeded {
		return stdout.buf.String(), true, nil
	}
	return strings.TrimRight(stdout.buf.String(), "\n"), false, nil
}

// limitWriter buffers up to limit bytes (no limit when limit <= 0) and calls
// stop when more data arrives.
type limitWriter struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
	stop     context.CancelFunc
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		return w.buf.Write(p)
	}
	if w.exceeded {
		return len(p), nil
	}
	if room := w.limit - w.buf.Len(); len(p) > room {
		w.buf.Write(p[:room])
		w.exceeded = true
		w.stop()
		return len(p), nil
	}
	return w.buf.Write(p)
}

func sanitizeEnv() []string {
	dangerous := map[string]bool{
		"GIT_DIR":                          true,
		"GIT_WORK_TREE":                    true,
		"GIT_INDEX_FILE":                   true,
		"GIT_OBJECT_DIRECTORY":             true,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_ASKPASS":                      true,
		"GIT_SSH":                          true,
		"GIT_SSH_COMMAND":                  true,
		"GIT_CONFIG_GLOBAL":                true,
		"GIT_CONFIG_SYSTEM":                true,
		"GIT_CONFIG_NOSYSTEM":              true,
		"GIT_CONFIG":                       true,
		"GIT_EXEC_PATH":                    true,
		"GIT_PAGER":                        true,
		"GIT_EXTERNAL_DIFF":                true,
		"GIT_PROXY_COMMAND":                true,
		"GIT_CONFIG_COUNT":                 true,
		"GIT_CONFIG_PARAMETERS":            true,
		"GIT_NAMESPACE":                    true,
	}

	var env []string
	for _, e := range os.Environ() {
		key := strings.SplitN(e, "=", 2)[0]
		if !dangerous[key] {
			env = append(env, e)
		}
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	return env
}
