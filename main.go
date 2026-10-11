package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/config"
	"github.com/rophy/git-mcp/internal/git"
	"github.com/rophy/git-mcp/internal/server"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintf(os.Stderr, "Usage: git-mcp serve --config <path> [--stdio]\n")
		os.Exit(1)
	}

	var configPath string
	var useStdio bool
	for i, arg := range os.Args {
		if arg == "--config" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
		}
		if arg == "--stdio" {
			useStdio = true
		}
	}

	if configPath == "" {
		fmt.Fprintf(os.Stderr, "Error: --config is required\n")
		os.Exit(1)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	executor := git.NewExecutor(cfg.Server.GitTimeout)

	upstreamResolver := func(repo string) (string, string, error) {
		host := strings.SplitN(repo, "/", 2)[0]
		upstream, ok := cfg.Upstream[host]
		if !ok {
			return "", "", fmt.Errorf("no upstream configured for host %q", host)
		}
		return fmt.Sprintf("https://%s", repo), upstream.Token, nil
	}

	cacheMgr := cache.NewManager(cfg.Server.CacheDir, executor, upstreamResolver)
	srv := server.New(cfg, cacheMgr, executor, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if useStdio {
		logger.Info("starting in stdio mode")
		if err := server.RunStdio(ctx, srv); err != nil {
			logger.Error("stdio server error", "err", err)
			os.Exit(1)
		}
		return
	}

	listen := cfg.Server.Listen
	if listen == "" {
		listen = "0.0.0.0:8080"
	}
	if err := server.RunHTTP(ctx, srv, listen, logger); err != nil {
		logger.Error("http server error", "err", err)
		os.Exit(1)
	}
}
