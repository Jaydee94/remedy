// Command remedy-runner executes agent CLI subprocesses on behalf of the control plane.
//
// The runner holds the CLI logins and nothing else. It dials the control plane
// (outbound only), pulls jobs and never reads or copies CLI credentials.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install-cli" {
		os.Exit(installCLI(os.Args[2:]))
	}

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

	if n, err := runner.SweepWorkspaces(cfg.WorkspaceRoot); err != nil {
		log.Warn("cannot remove everything an earlier runner left behind", "path", cfg.WorkspaceRoot, "removed", n, "err", err)
	} else if n > 0 {
		log.Info("removed what an earlier runner left behind", "path", cfg.WorkspaceRoot, "directories", n)
	}

	claude := provider.Claude{Binary: cfg.ClaudeBin, Model: cfg.ClaudeModel}
	status := runner.NewStatus()
	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: cfg.ServerURL, Token: cfg.Token, HTTP: &http.Client{}},
		Providers:     map[string]provider.Provider{"claude": claude},
		Status:        status,
		WorkspaceRoot: cfg.WorkspaceRoot,
		Env:           os.Environ(),
		Log:           log,
		RunTimeout:    cfg.RunTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.StatusAddr != "" {
		// Bind before anything else starts: a runner that cannot serve its probes must not run on without them.
		ln, err := net.Listen("tcp", cfg.StatusAddr)
		if err != nil {
			log.Error("cannot listen for the status probes", "addr", cfg.StatusAddr, "err", err)
			os.Exit(1)
		}
		statusSrv := &http.Server{Handler: runner.StatusHandler(status), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := statusSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("status listener failed", "err", err)
				os.Exit(1)
			}
		}()
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = statusSrv.Shutdown(shutdown)
		}()
		log.Info("status listener", "addr", ln.Addr().String())
	}
	go (&runner.Reporter{Client: loop.Client, Status: status, Log: log}).Run(ctx)
	go (&runner.LoginChecker{
		Check:   func(c context.Context) (provider.LoginState, string) { return claude.LoginCheck(c, os.Environ()) },
		Version: func(c context.Context) string { return claude.Version(c, os.Environ()) },
		Status:  status,
		Log:     log,
	}).Run(ctx)

	log.Info("runner started", "server", cfg.ServerURL, "workspaces", cfg.WorkspaceRoot)
	loop.Run(ctx)
	log.Info("runner stopped")
}
