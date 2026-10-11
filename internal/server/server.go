package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rophy/git-mcp/internal/audit"
	"github.com/rophy/git-mcp/internal/auth"
	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/config"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/tools"
)

func addTool[In, Out any](srv *mcp.Server, authenticator *auth.Authenticator, auditLog *audit.Logger, name, desc string, exec func(context.Context, In) (*Out, error)) {
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: desc},
		func(ctx context.Context, req *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
			start := time.Now()
			entry := auditEntry(name, input)
			entry.Service = audit.ServiceFromContext(ctx)
			entry.RequestID = audit.RequestIDFromContext(ctx)

			result, err := checkAccess(authenticator, entry.Service, entry.Repo)
			var out Out
			if err == nil {
				var res *Out
				if res, err = exec(ctx, input); err == nil {
					out = *res
					entry.ResolvedSHA = resolvedSHA(out)
				}
			}

			entry.Duration = time.Since(start)
			if err != nil {
				entry.Error = err.Error()
			}
			auditLog.Log(entry)
			return result, out, err
		})
}

// checkAccess denies the call unless auth is off or the service may read repo.
// It fails closed: with auth on, a call without a service is denied.
func checkAccess(authenticator *auth.Authenticator, service, repo string) (*mcp.CallToolResult, error) {
	if authenticator == nil {
		return nil, nil
	}
	if service == "" || !authenticator.CheckRepoAccess(service, repo) {
		return nil, fmt.Errorf("access denied to repo %s", repo)
	}
	return nil, nil
}

// auditEntry reads the logged fields from the tool input's JSON form.
func auditEntry(tool string, input any) audit.Entry {
	fields := jsonFields(input)
	ref := fields["ref"]
	if ref == "" && fields["head"] != "" {
		ref = fields["base"] + "..." + fields["head"]
	}
	return audit.Entry{Tool: tool, Repo: fields["repo"], Ref: ref, Path: fields["path"]}
}

// resolvedSHA reads the resolved commit from the tool output's JSON form.
func resolvedSHA(out any) string {
	fields := jsonFields(out)
	if fields["sha"] != "" {
		return fields["sha"]
	}
	return fields["head_sha"]
}

func jsonFields(v any) map[string]string {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var all map[string]any
	if json.Unmarshal(raw, &all) != nil {
		return nil
	}
	fields := map[string]string{}
	for k, v := range all {
		if s, ok := v.(string); ok {
			fields[k] = s
		}
	}
	return fields
}

func New(cfg *config.Config, cacheMgr *cache.Manager, executor *git.Executor, authenticator *auth.Authenticator, logger *slog.Logger) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "git-mcp", Version: "0.1.0"}, nil)
	auditLog := audit.NewLogger(logger)

	addTool(srv, authenticator, auditLog, "resolve_ref",
		"Resolve a git ref (branch, tag, SHA) to a commit SHA. Use refresh=true to fetch latest from upstream.",
		tools.NewResolveRef(cacheMgr).Execute)
	addTool(srv, authenticator, auditLog, "search_code",
		"Search code with regex across a repository. Returns matching lines with file paths and line numbers.",
		tools.NewSearchCode(cacheMgr, executor, cfg.Server.MaxMatches, cfg.Server.MaxOutputBytes).Execute)
	addTool(srv, authenticator, auditLog, "read_file",
		"Read file contents at a specific ref. Returns numbered lines. Supports line range selection.",
		tools.NewReadFile(cacheMgr, executor, cfg.Server.MaxFileBytes).Execute)
	addTool(srv, authenticator, auditLog, "list_files",
		"List files and directories at a path in a repository.",
		tools.NewListFiles(cacheMgr, executor, cfg.Server.MaxOutputBytes).Execute)
	addTool(srv, authenticator, auditLog, "git_log",
		"Show commit history for a repository, optionally filtered by path.",
		tools.NewGitLog(cacheMgr, executor, cfg.Server.MaxOutputBytes).Execute)
	addTool(srv, authenticator, auditLog, "git_show",
		"Show a commit's metadata and diff.",
		tools.NewGitShow(cacheMgr, executor, cfg.Server.MaxDiffBytes).Execute)
	addTool(srv, authenticator, auditLog, "git_diff",
		"Show the diff between two refs. Set merge_base=true to diff from their common ancestor (pull-request style).",
		tools.NewGitDiff(cacheMgr, executor, cfg.Server.MaxDiffBytes).Execute)
	addTool(srv, authenticator, auditLog, "git_blame",
		"Show which commit last changed each line of a file. Supports line range selection.",
		tools.NewGitBlame(cacheMgr, executor, cfg.Server.MaxOutputBytes).Execute)

	return srv
}

func RunStdio(ctx context.Context, srv *mcp.Server) error {
	return srv.Run(ctx, &mcp.StdioTransport{})
}

func RunHTTP(ctx context.Context, srv *mcp.Server, listen string, authenticator *auth.Authenticator, logger *slog.Logger) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	return ServeHTTP(ctx, srv, ln, authenticator, logger)
}

// ServeHTTP serves MCP over HTTP on ln until ctx is done.
func ServeHTTP(ctx context.Context, srv *mcp.Server, ln net.Listener, authenticator *auth.Authenticator, logger *slog.Logger) error {
	// Stateless (see design spec): no server-side sessions and no long-lived
	// GET streams, so shutdown only waits for in-flight tool calls.
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true})

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	var h http.Handler = mux
	if authenticator != nil {
		h = auth.HTTPMiddleware(authenticator, logger)(h)
	} else {
		logger.Warn("authentication disabled: no auth issuers configured, all clients can access all repos")
	}
	h = audit.Middleware(h)

	conns := &newConns{conns: map[net.Conn]struct{}{}}
	httpSrv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ConnState: conns.track}
	// Shutdown waits up to 5s for connections that have not sent a request
	// yet; close them as soon as shutdown starts.
	httpSrv.RegisterOnShutdown(conns.closeAll)
	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownDone <- httpSrv.Shutdown(shutCtx)
	}()
	logger.Info("starting HTTP server", "listen", ln.Addr().String())
	if err := httpSrv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Serve returns as soon as Shutdown starts; wait for in-flight requests.
	return <-shutdownDone
}

// newConns tracks connections that have not sent a request yet.
type newConns struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func (c *newConns) track(conn net.Conn, state http.ConnState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state == http.StateNew {
		c.conns[conn] = struct{}{}
	} else {
		delete(c.conns, conn)
	}
}

func (c *newConns) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for conn := range c.conns {
		conn.Close()
	}
}
