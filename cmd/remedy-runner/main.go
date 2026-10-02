// Command remedy-runner executes agent CLI subprocesses on behalf of the control plane.
//
// The runner holds the CLI logins and nothing else. It dials the control plane
// (outbound only), pulls jobs and never reads or copies CLI credentials.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("runner started (no job polling implemented yet)")
	<-ctx.Done()
	log.Info("runner stopped")
}
