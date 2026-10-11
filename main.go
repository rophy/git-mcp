package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rophy/git-mcp/internal/auth"
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

	for _, host := range cfg.InsecureUpstreams() {
		logger.Warn("upstream password is sent over plain http", "host", host)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cacheMgr, err := cache.NewManager(cfg.Server.CacheDir, cfg.Server.MaxCacheBytes, executor, cfg.UpstreamURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	var authenticator *auth.Authenticator
	if !useStdio && len(cfg.Auth.Issuers) > 0 {
		authenticator, err = auth.NewAuthenticator(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing auth: %v\n", err)
			os.Exit(1)
		}
	}

	srv := server.New(cfg, cacheMgr, executor, authenticator, logger)

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
	if err := server.RunHTTP(ctx, srv, listen, authenticator, logger); err != nil {
		logger.Error("http server error", "err", err)
		os.Exit(1)
	}
}
