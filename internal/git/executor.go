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
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = repoPath
	cmd.Env = append(sanitizeEnv(), extraEnv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git command timed out after %s", e.timeout)
		}
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), stderr.String(), err)
	}

	return strings.TrimRight(stdout.String(), "\n"), nil
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
