package kube

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
)

// maxBody bounds an answer. The tools cut what they show far below it.
const maxBody = 8 << 20

// maxMessage bounds the cluster's own error message that goes into an error.
const maxMessage = 300

// requestTimeout is the time one request may take. It is a variable only so that a test does not wait for it.
var requestTimeout = 15 * time.Second

var (
	// ErrNotFound, ErrForbidden, ErrUnauthorized and ErrConflict are the answers 404, 403, 401 and 409. An *APIError
	// of that status wraps the matching one.
	ErrNotFound     = errors.New("kube: not found")
	ErrForbidden    = errors.New("kube: forbidden")
	ErrUnauthorized = errors.New("kube: unauthorized")
	ErrConflict     = errors.New("kube: conflict")
	// ErrTooLarge means the answer is bigger than the client reads.
	ErrTooLarge = errors.New("kube: the answer is too large")
	// ErrInvalid means an argument cannot be used in a request path; nothing was sent.
	ErrInvalid = errors.New("kube: invalid argument")
	// ErrNamespaceNotAllowed means the namespace is not in the allowlist; nothing was sent.
	ErrNamespaceNotAllowed = errors.New("kube: the namespace is not in the allowlist")
)

// APIError is a non-success answer of the API server.
type APIError struct {
	Status  int
	Reason  string
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("kube: HTTP %d", e.Status)
	}
	return fmt.Sprintf("kube: HTTP %d: %s", e.Status, e.Message)
}

func (e *APIError) Unwrap() error {
	switch e.Status {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusConflict:
		return ErrConflict
	}
	return nil
}

// requestError is a failure to talk to the API server, with the token taken out of its text.
type requestError struct {
	msg string
	err error
}

func (e *requestError) Error() string { return e.msg }
func (e *requestError) Unwrap() error { return e.err }

// Option changes how a client is built.
type Option func(*options)

type options struct{ log *slog.Logger }

// WithLog makes a client log every request at debug level: method, path, status and duration. Never a query, a
// header or a body.
func WithLog(log *slog.Logger) Option { return func(o *options) { o.log = log } }

// guard is the transport of a client. It is the second line of defence behind the methods of the client: whatever
// calls it, only the methods the client is meant to send leave the process.
type guard struct {
	base  http.RoundTripper
	allow func(method string) bool
	what  string
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if !g.allow(req.Method) {
		return nil, fmt.Errorf("kube: refusing to send %s: %s", req.Method, g.what)
	}
	return g.base.RoundTrip(req)
}

type client struct {
	base      string
	tokenFile string
	http      *http.Client
	log       *slog.Logger
}

func newClient(c Config, tokenFile string, allow func(string) bool, what string, opts []Option) (*client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	pool, err := loadPool(c.CAFile)
	if err != nil {
		return nil, err
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // cluster traffic goes straight to the API server, whatever the environment says
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &client{
		base:      c.api(),
		tokenFile: tokenFile,
		log:       o.log,
		http: &http.Client{
			Timeout:   requestTimeout,
			Transport: &guard{base: tr, allow: allow, what: what},
			// An answer that redirects is an answer, not an instruction to send the token elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// readToken reads a token file. It is called for every request: a projected service account token is replaced
// by the kubelet before it expires.
func readToken(path string) (secret.Value, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return secret.Value{}, err
	}
	tok := strings.TrimSpace(string(raw))
	switch {
	case tok == "":
		return secret.Value{}, fmt.Errorf("the token file %s is empty", path)
	case strings.ContainsAny(tok, "\r\n"):
		return secret.Value{}, fmt.Errorf("the token file %s holds more than one line", path)
	}
	return secret.NewValue(tok), nil
}

func scrub(s string, token secret.Value) string {
	if t := token.Reveal(); t != "" {
		s = strings.ReplaceAll(s, t, "***")
	}
	return s
}

// do sends one request and returns the body of a 2xx answer.
func (c *client) do(ctx context.Context, method, path string, query url.Values, contentType string, body []byte) ([]byte, error) {
	token, err := readToken(c.tokenFile)
	if err != nil {
		return nil, err
	}
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var payload io.Reader = http.NoBody
	if body != nil {
		payload = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return nil, &requestError{msg: scrub(err.Error(), token), err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token.Reveal())
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.logRequest(method, path, 0, started)
		return nil, &requestError{msg: scrub(err.Error(), token), err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	c.logRequest(method, path, resp.StatusCode, started)
	if err != nil {
		return nil, &requestError{msg: scrub(err.Error(), token), err: err}
	}
	if len(raw) > maxBody {
		return nil, ErrTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, statusError(resp.StatusCode, raw, token)
	}
	return raw, nil
}

func (c *client) logRequest(method, path string, status int, started time.Time) {
	if c.log == nil {
		return
	}
	c.log.Debug("kubernetes request", "method", method, "path", path, "status", status, "took", time.Since(started).Round(time.Millisecond))
}

// statusError turns a non-success answer into an error. The API server explains itself in a Status object; any
// other body is not shown.
func statusError(status int, body []byte, token secret.Value) error {
	e := &APIError{Status: status}
	var st struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &st) == nil {
		e.Reason = st.Reason
		e.Message = scrub(st.Message, token)
		if len(e.Message) > maxMessage {
			e.Message = strings.ToValidUTF8(e.Message[:maxMessage], "") + "..."
		}
	}
	return e
}
