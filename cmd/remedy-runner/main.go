// Command remedy-runner executes agent CLI subprocesses on behalf of the control plane.
//
// The runner holds the CLI logins and nothing else. It dials the control plane
// (outbound only), pulls jobs and never reads or copies CLI credentials.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.RunnerFromEnv(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(2)
	}
	if err := os.MkdirAll(cfg.WorkspaceRoot, 0o700); err != nil {
		log.Error("cannot create workspace root", "path", cfg.WorkspaceRoot, "err", err)
		os.Exit(1)
	}

	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: cfg.ServerURL, Token: cfg.Token, HTTP: &http.Client{}},
		Providers:     map[string]provider.Provider{"claude": provider.Claude{Binary: cfg.ClaudeBin, Model: cfg.ClaudeModel}},
		WorkspaceRoot: cfg.WorkspaceRoot,
		Env:           os.Environ(),
		Log:           log,
		RunTimeout:    cfg.RunTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("runner started", "server", cfg.ServerURL, "workspaces", cfg.WorkspaceRoot)
	loop.Run(ctx)
	log.Info("runner stopped")
}
