// Command remedy-server runs the Remedy control plane.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.ServerFromEnv(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(2)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("cannot open database", "path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	defer st.Close()

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: server.New(server.Deps{
			Store:       st,
			Auth:        auth.New(cfg.AdminPassword),
			RunnerToken: cfg.RunnerToken,
			Web:         web.FS(),
			Key:         cfg.MasterKey,
			NewGitHub: func(token secret.Value) server.GitHub {
				return github.New(cfg.GitHubAPIURL, token, nil)
			},
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("control plane listening", "addr", cfg.Addr, "db", cfg.DBPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}
