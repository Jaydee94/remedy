package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/snapshot"
)

const (
	// DefaultRunTimeout is how long a run may take before the runner stops it.
	DefaultRunTimeout = 10 * time.Minute

	// snapshotTimeout bounds downloading and unpacking the repository snapshot of a responder run.
	snapshotTimeout = 2 * time.Minute
)

// Loop claims runs one at a time, executes them and reports the outcome. A runner that dies
// mid-run leaves its run "running"; the control plane's reaper fails it after a while.
type Loop struct {
	Client            *Client
	Providers         map[string]provider.Provider
	WorkspaceRoot     string
	Env               []string
	Log               *slog.Logger
	Backoff           time.Duration // wait after a failed claim, default 3s
	RunTimeout        time.Duration // longest a run may take while it does not wait for an approval, default DefaultRunTimeout
	HeartbeatInterval time.Duration // how often a run with tools reports to the control plane, default DefaultHeartbeatInterval
	Status            *Status       // optional: told whether the control plane answers the claim
}

func (l *Loop) Run(ctx context.Context) {
	backoff := l.Backoff
	if backoff == 0 {
		backoff = 3 * time.Second
	}
	for ctx.Err() == nil {
		r, err := l.Client.Claim(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			l.Status.SetConnected(false)
			l.Log.Warn("claim failed", "err", err)
			sleep(ctx, backoff)
			continue
		}
		l.Status.SetConnected(true)
		if r == nil {
			continue // long-poll timed out, ask again
		}
		l.handle(ctx, *r)
	}
}

func (l *Loop) handle(ctx context.Context, c run.Claim) {
	r := c.Run
	log := l.Log.With("run", r.ID, "provider", r.Provider)
	log.Info("run claimed")

	p, ok := l.Providers[r.Provider]
	if !ok {
		l.finish(log, r.ID, run.Outcome{ExitCode: 127, Result: "unknown provider " + r.Provider})
		return
	}

	dir, err := os.MkdirTemp(l.WorkspaceRoot, r.ID+"-")
	if err != nil {
		l.finish(log, r.ID, run.Outcome{ExitCode: 127, Result: "cannot create workspace: " + err.Error()})
		return
	}
	defer os.RemoveAll(dir)

	if c.Snapshot {
		if err := l.fetchSnapshot(ctx, log, r.ID, dir); err != nil {
			log.Error("snapshot failed", "err", err)
			l.finish(log, r.ID, run.Outcome{ExitCode: 1, Result: "The repository snapshot could not be prepared: " + err.Error()})
			return
		}
	}

	spec := provider.Spec{Prompt: r.Prompt, Workdir: dir, Schema: string(c.Schema)}
	if c.MCPToken != "" {
		path, cleanup, err := writeMCPConfig(l.WorkspaceRoot, r.ID, l.Client.BaseURL, c.MCPToken)
		if err != nil {
			log.Error("cannot write the MCP config", "err", err)
			l.finish(log, r.ID, run.Outcome{ExitCode: 127, Result: "The tools of the run could not be prepared."})
			return
		}
		defer cleanup()
		spec.MCPConfig = path
	}

	timeout := l.RunTimeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	// The time limit is a budget of running time. A run that waits for an approval does not use it up, and the
	// control plane can cancel the run: both reach the runner through the heartbeat of a run with tools.
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	clock := newRunClock(timeout, cancel)
	defer clock.Stop()
	var stopped atomic.Bool // the control plane cancelled the run, or no longer has it
	if c.MCPToken != "" {
		go l.watch(execCtx, log, r.ID, clock, func() { stopped.Store(true); cancel() })
	}
	out, err := Execute(execCtx, p, spec, l.Env, clientSink{client: l.Client, runID: r.ID})
	if err != nil {
		log.Error("execution problem", "err", err)
	}
	switch {
	case stopped.Load():
		out.FailureReason = run.ReasonCancelled
		out.Result = "The run was cancelled."
		if out.ExitCode == 0 {
			out.ExitCode = -1
		}
	// The budget kills the subprocess. A run that ended by itself is not a timeout, even if the budget ran out a
	// moment later; neither is a run that was cut short by the runner shutting down.
	case ctx.Err() == nil && clock.Expired() && out.ExitCode != 0:
		out.FailureReason = run.ReasonTimeout
		if out.Result == "" {
			out.Result = fmt.Sprintf("The run was stopped after %s.", timeout)
		}
		log.Warn("run timed out", "after", timeout)
	}
	l.finish(log, r.ID, out)
}

// fetchSnapshot downloads the repository snapshot of a responder run into dir. snapshot.Unpack trusts
// nothing: it refuses path traversal, extracts only harmless symlinks and enforces the size limits.
func (l *Loop) fetchSnapshot(ctx context.Context, log *slog.Logger, runID, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	rc, err := l.Client.Snapshot(ctx, runID)
	if err != nil {
		return err
	}
	defer rc.Close()
	res, err := snapshot.Unpack(dir, rc, snapshot.Limits{})
	if err != nil {
		return err
	}
	log.Info("snapshot unpacked", "files", res.Files, "bytes", res.Bytes, "skipped", len(res.Skipped))
	return nil
}

// finish reports the outcome even when the runner is shutting down.
func (l *Loop) finish(log *slog.Logger, runID string, out run.Outcome) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := l.Client.Finish(ctx, runID, out); err != nil {
		log.Error("finish failed", "err", err)
		return
	}
	log.Info("run finished", "exit", out.ExitCode)
}

type clientSink struct {
	client *Client
	runID  string
}

func (s clientSink) Event(ctx context.Context, kind string, payload json.RawMessage) error {
	return s.client.Event(ctx, s.runID, kind, payload)
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
