// Command remedy-server runs the Remedy control plane.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	level, err := config.LogLevelFromEnv(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(2)
	}
	log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := config.ServerFromEnv(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Open waits up to 30 s for a database that is still starting; a signal during that wait ends it. No error of Open holds the
	// URL, and the log names only host:port/dbname.
	log.Info("connecting to the database", "database", cfg.DatabaseTarget())
	st, err := store.Open(ctx, cfg.DatabaseURL, store.WithMaxConns(cfg.DatabaseMaxConns), store.WithLogger(log))
	if err != nil {
		log.Error("cannot open database", "database", cfg.DatabaseTarget(), "err", err)
		stop()
		os.Exit(1)
	}
	defer st.Close()

	// Background workers stop with ctx. They are waited for before the database closes.
	var workers sync.WaitGroup
	defer workers.Wait()
	background := func(run func(context.Context)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			run(ctx)
		}()
	}

	for _, w := range cfg.Cluster.Warnings() {
		log.Warn(w)
	}

	a := app.New(cfg, st, log, web.FS())
	background(a.CheckCluster)
	background(a.Poller.Run)
	background(a.Signals.Run)
	background(a.Probe.Run)
	background(a.Reaper.Run)
	background(a.Login.Run) // the login relay has no switch: app.New always builds it

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	var internal *http.Server
	if a.InternalHandler != nil {
		internal = &http.Server{
			Addr:              cfg.InternalAddr,
			Handler:           a.InternalHandler,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			log.Info("internal listener", "addr", cfg.InternalAddr)
			if err := internal.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("internal listener failed", "err", err)
				os.Exit(1)
			}
		}()
	}

	// ListenAndServe returns as soon as Shutdown starts, while handlers may still be draining: main waits for shutdownDone
	// before the deferred workers.Wait and st.Close, so no handler meets a closed store.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		if internal != nil {
			_ = internal.Shutdown(shutdownCtx)
		}
	}()

	log.Info("control plane listening", "addr", cfg.Addr, "database", cfg.DatabaseTarget(), "pollInterval", a.Settings.Duration(settings.PollInterval))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
	<-shutdownDone
}
