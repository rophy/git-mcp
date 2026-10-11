package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rophy/git-mcp/internal/audit"
	"github.com/rophy/git-mcp/internal/auth"
	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/config"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/tools"
)

type repoInput interface{ GetRepo() string }

func addTool[In, Out any](srv *mcp.Server, authenticator *auth.Authenticator, name, desc string, exec func(context.Context, In) (*Out, error)) {
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: desc},
		func(ctx context.Context, req *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
			if authenticator != nil {
				if service := audit.ServiceFromContext(ctx); service != "" {
					ri, ok := any(input).(repoInput)
					if !ok {
						ri, ok = any(&input).(repoInput)
					}
					if !ok || !authenticator.CheckRepoAccess(service, ri.GetRepo()) {
						var zero Out
						repo := ""
						if ok {
							repo = ri.GetRepo()
						}
						return nil, zero, fmt.Errorf("access denied to repo %s", repo)
					}
				}
			}
			result, err := exec(ctx, input)
			if err != nil {
				var zero Out
				return nil, zero, err
			}
			return nil, *result, nil
		})
}

func New(cfg *config.Config, cacheMgr *cache.Manager, executor *git.Executor, authenticator *auth.Authenticator, logger *slog.Logger) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "git-mcp", Version: "0.1.0"}, nil)
	maxOutputSize := parseSize(cfg.Server.MaxOutputSize, 1024*1024)

	addTool(srv, authenticator, "resolve_ref",
		"Resolve a git ref (branch, tag, SHA) to a commit SHA. Use refresh=true to fetch latest from upstream.",
		tools.NewResolveRef(cacheMgr).Execute)
	addTool(srv, authenticator, "search_code",
		"Search code with regex across a repository. Returns matching lines with file paths and line numbers.",
		tools.NewSearchCode(cacheMgr, executor, cfg.Server.MaxMatches, maxOutputSize).Execute)
	addTool(srv, authenticator, "read_file",
		"Read file contents at a specific ref. Returns numbered lines. Supports line range selection.",
		tools.NewReadFile(cacheMgr, executor, parseSize(cfg.Server.MaxFileSize, 1024*1024)).Execute)
	addTool(srv, authenticator, "list_files",
		"List files and directories at a path in a repository.",
		tools.NewListFiles(cacheMgr, executor, maxOutputSize).Execute)
	addTool(srv, authenticator, "git_log",
		"Show commit history for a repository, optionally filtered by path.",
		tools.NewGitLog(cacheMgr, executor, maxOutputSize).Execute)
	addTool(srv, authenticator, "git_show",
		"Show a commit's metadata and diff.",
		tools.NewGitShow(cacheMgr, executor, parseSize(cfg.Server.MaxDiffSize, 500*1024)).Execute)

	return srv
}

func RunStdio(ctx context.Context, srv *mcp.Server) error {
	return srv.Run(ctx, &mcp.StdioTransport{})
}

func RunHTTP(ctx context.Context, srv *mcp.Server, listen string, authenticator *auth.Authenticator, logger *slog.Logger) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)

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

	httpSrv := &http.Server{Addr: listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutCtx)
	}()
	logger.Info("starting HTTP server", "listen", listen)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func parseSize(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	var val int
	var unit string
	fmt.Sscanf(s, "%d%s", &val, &unit)
	switch unit {
	case "KB":
		return val * 1024
	case "MB":
		return val * 1024 * 1024
	case "GB":
		return val * 1024 * 1024 * 1024
	}
	return val
}
