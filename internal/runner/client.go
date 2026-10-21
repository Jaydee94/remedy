package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Jaydee94/remedy/internal/loginrelay"
	"github.com/Jaydee94/remedy/internal/run"
)

const (
	claimTimeout   = 40 * time.Second // server long-polls for 25 s
	requestTimeout = 15 * time.Second
)

// Client talks to the control plane's runner API. The runner only ever dials out.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (c *Client) do(ctx context.Context, timeout time.Duration, path string, body any) (*http.Response, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, &buf)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func expect(resp *http.Response, want int) error {
	if resp.StatusCode == want {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
}

// Claim blocks until a run is available or the server's long-poll times out (nil, nil).
func (c *Client) Claim(ctx context.Context) (*run.Claim, error) {
	resp, err := c.do(ctx, claimTimeout, "/runner/v1/claim", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if err := expect(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var r run.Claim
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) Event(ctx context.Context, runID, kind string, payload json.RawMessage) error {
	resp, err := c.do(ctx, requestTimeout, "/runner/v1/runs/"+runID+"/events",
		map[string]any{"kind": kind, "payload": payload})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expect(resp, http.StatusNoContent)
}

// Snapshot streams the repository snapshot of a responder run (a gzipped tar). The caller closes it and
// sets the time limit through ctx; there is no overall timeout, because the archive can be large.
func (c *Client) Snapshot(ctx context.Context, runID string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/runner/v1/runs/"+runID+"/snapshot", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if err := expect(resp, http.StatusOK); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) Finish(ctx context.Context, runID string, o run.Outcome) error {
	resp, err := c.do(ctx, requestTimeout, "/runner/v1/runs/"+runID+"/finish", o)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expect(resp, http.StatusNoContent)
}

// ErrLoginAbsent says that the control plane has no login relay (its route answered 404).
var ErrLoginAbsent = errors.New("runner: the control plane has no login relay")

// errLoginAnswer is every unreadable poll answer. It never quotes the body, which may hold the code.
var errLoginAnswer = errors.New("runner: unreadable login poll answer")

// maxLoginAnswer bounds a poll answer: a code is at most 512 bytes.
const maxLoginAnswer = 4 << 10

// LoginPoll is the relay's long poll: nil when the control plane has no command (204), ErrLoginAbsent when it has no
// relay (404). The answer may carry the code: the caller wraps it in a Material at once. The body buffer is cleared.
func (c *Client) LoginPoll(ctx context.Context, req loginrelay.PollRequest) (*loginrelay.PollAnswer, error) {
	resp, err := c.do(ctx, claimTimeout, "/runner/v1/login/poll", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusNotFound:
		return nil, ErrLoginAbsent
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("login poll: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLoginAnswer+1))
	defer clear(body)
	if err != nil || len(body) > maxLoginAnswer {
		return nil, errLoginAnswer
	}
	var a loginrelay.PollAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, errLoginAnswer
	}
	return &a, nil
}

// LoginReport posts the relay's state report. The error names a status code, never the body of either side.
func (c *Client) LoginReport(ctx context.Context, r loginrelay.Report) error {
	resp, err := c.do(ctx, requestTimeout, "/runner/v1/login/report", r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("login report: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// ReportStatus tells the control plane the runner's login state. It is best effort: the reporter decides what a failure means.
func (c *Client) ReportStatus(ctx context.Context, r Report) error {
	resp, err := c.do(ctx, requestTimeout, "/runner/v1/status", r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expect(resp, http.StatusNoContent)
}
