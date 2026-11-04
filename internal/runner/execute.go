// Package runner executes agent CLI subprocesses and reports them to the control plane.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
)

// maxLine bounds one line of CLI output (stream-json lines can carry whole files).
const maxLine = 4 << 20

// Sink receives the events of one run.
type Sink interface {
	Event(ctx context.Context, kind string, payload json.RawMessage) error
}

// Execute runs the provider subprocess to completion, forwarding every stdout line (parsed)
// and every stderr line to sink. It always returns a usable Outcome. A non-nil error means
// the subprocess could not be started (ExitCode 127) or the first sink error (events may be
// missing); in both cases the run still ended with the returned Outcome.
func Execute(ctx context.Context, p provider.Provider, spec provider.Spec, env []string, sink Sink) (run.Outcome, error) {
	// The run can be stopped from inside: by the billing guard, when the CLI's init event contradicts the mode.
	ctx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	cmd := p.Command(ctx, spec, env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return failedToStart(err), err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return failedToStart(err), err
	}
	if err := cmd.Start(); err != nil {
		return failedToStart(err), fmt.Errorf("start %s: %w", p.Name(), err)
	}

	var (
		mu      sync.Mutex
		sinkErr error
	)
	emit := func(kind string, payload json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()
		if err := sink.Event(ctx, kind, payload); err != nil && sinkErr == nil {
			sinkErr = err
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), maxLine)
		for sc.Scan() {
			emit("stderr", run.JSONString(sc.Text()))
		}
		_, _ = io.Copy(io.Discard, stderr) // keep draining if a line was too long
	}()

	var (
		final      *provider.Final
		authSource string
		billingBad bool
		initSeen   bool
	)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		l := p.ParseLine(line)
		if l.Final != nil {
			final = l.Final
		}
		// The guard looks at the line before it is posted: posting is a round trip to the control plane, and the CLI
		// sends its first request right after the init event.
		if !billingBad {
			switch {
			case l.Init != nil && !initSeen:
				initSeen = true
				authSource = provider.AuthSourceOf(l.Init.APIKeySource, l.Init.Known)
				if spec.OnInit != nil && !spec.OnInit(l.Init.APIKeySource, l.Init.Known) {
					billingBad = true
					stopRun()
				}
			case spec.RequireInit && !initSeen && l.Kind != "system" && l.Kind != "raw":
				// The CLI went on without saying how it is billed.
				billingBad = true
				stopRun()
			}
		}
		emit(l.Kind, l.Payload)
	}
	if err := sc.Err(); err != nil {
		emit("raw", run.JSONString(fmt.Sprintf("stdout read error: %v", err)))
		_, _ = io.Copy(io.Discard, stdout)
	}
	wg.Wait()

	exit := 0
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode() // -1 if the process was killed by a signal
		} else {
			exit = -1
		}
	}

	// A run that ended well without ever proving its billing is refused too; one that failed or was stopped for another reason
	// keeps that reason.
	if spec.RequireInit && !initSeen && !billingBad && exit == 0 && ctx.Err() == nil {
		billingBad = true
	}

	out := run.Outcome{ExitCode: exit, AuthSource: authSource}
	if final != nil {
		out.Result, out.SessionID, out.CostUSD = final.Result, final.SessionID, final.CostUSD
		out.Output = final.Output
	}
	if billingBad {
		// Whatever the CLI wrote after the init event does not count; the sentence is fixed and carries no CLI text.
		out = run.Outcome{ExitCode: exit, AuthSource: authSource, FailureReason: run.ReasonBillingMismatch,
			Result: "The run was stopped: it would have been billed differently than configured."}
		if out.ExitCode == 0 {
			out.ExitCode = -1
		}
	}
	return out, sinkErr
}

func failedToStart(err error) run.Outcome {
	return run.Outcome{ExitCode: 127, Result: fmt.Sprintf("failed to start: %v", err)}
}
