package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
)

// Loop claims runs one at a time, executes them and reports the outcome.
// Known limitation (phase 0): if the runner dies mid-run the run stays "running";
// a reaper for stale runs arrives with phase 1.
type Loop struct {
	Client        *Client
	Providers     map[string]provider.Provider
	WorkspaceRoot string
	Env           []string
	Log           *slog.Logger
	Backoff       time.Duration // wait after a failed claim, default 3s
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
			l.Log.Warn("claim failed", "err", err)
			sleep(ctx, backoff)
			continue
		}
		if r == nil {
			continue // long-poll timed out, ask again
		}
		l.handle(ctx, *r)
	}
}

func (l *Loop) handle(ctx context.Context, r run.Run) {
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

	out, err := Execute(ctx, p, provider.Spec{Prompt: r.Prompt, Workdir: dir}, l.Env,
		clientSink{client: l.Client, runID: r.ID})
	if err != nil {
		log.Error("execution problem", "err", err)
	}
	l.finish(log, r.ID, out)
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
