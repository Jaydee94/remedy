package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

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
func (c *Client) Claim(ctx context.Context) (*run.Run, error) {
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
	var r run.Run
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

func (c *Client) Finish(ctx context.Context, runID string, o run.Outcome) error {
	resp, err := c.do(ctx, requestTimeout, "/runner/v1/runs/"+runID+"/finish", o)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expect(resp, http.StatusNoContent)
}
