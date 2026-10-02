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

	var final *provider.Final
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

	out := run.Outcome{ExitCode: exit}
	if final != nil {
		out.Result, out.SessionID, out.CostUSD = final.Result, final.SessionID, final.CostUSD
	}
	return out, sinkErr
}

func failedToStart(err error) run.Outcome {
	return run.Outcome{ExitCode: 127, Result: fmt.Sprintf("failed to start: %v", err)}
}
