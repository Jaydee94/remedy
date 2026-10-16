// Package alertmanager is Remedy's read-only client of an Alertmanager. It asks for the active alerts and nothing else: every
// exported method is a Get or a List, the transport refuses every method but GET and HEAD before it is sent, redirects are not
// followed and no proxy is used. The address and the token are asked for at every request, because the Setup page can change
// them while the control plane runs.
package alertmanager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

// maxBody bounds an answer: a bigger one is an error, never a partial list.
const maxBody = 8 << 20

// requestTimeout is the time one request may take. It is a variable only so that a test does not wait for it.
var requestTimeout = 15 * time.Second

var (
	// ErrNotConfigured means no address is set. ErrTooLarge means the answer is bigger than the client reads. ErrNotAlerts means
	// the answer is not a JSON array of alerts.
	ErrNotConfigured = errors.New("alertmanager: no address is set")
	ErrTooLarge      = errors.New("alertmanager: the answer is too large")
	ErrNotAlerts     = errors.New("alertmanager: the answer is not a JSON array of alerts")
)

// APIError is an answer that is not a success. The body is not shown: it is text of another system.
type APIError struct{ Status int }

func (e *APIError) Error() string { return fmt.Sprintf("alertmanager: HTTP %d", e.Status) }

// Alert is one alert of the v2 API.
type Alert struct {
	Fingerprint  string            `json:"fingerprint"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Status       struct {
		State string `json:"state"`
	} `json:"status"`
}

// Credentials gives the address and the token of this moment. An empty token means none is sent.
type Credentials func() (baseURL string, token secret.Value, err error)

// Option changes how a client is built.
type Option func(*Client)

// WithLog makes the client log every request at debug level: path, status and duration. Never a header, a query or a body.
func WithLog(l *slog.Logger) Option { return func(c *Client) { c.log = l } }

// guard is the transport: whatever calls the client, only GET and HEAD leave the process.
type guard struct{ base http.RoundTripper }

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return nil, fmt.Errorf("alertmanager: refusing to send %s: this client only reads", req.Method)
	}
	return g.base.RoundTrip(req)
}

// Client asks an Alertmanager for its active alerts.
type Client struct {
	creds Credentials
	http  *http.Client
	log   *slog.Logger
}

// New builds a client. caFile is a PEM bundle for an https address; empty means the system's roots. creds is only called by
// ListAlerts.
func New(creds Credentials, caFile string, opts ...Option) (*Client, error) {
	var pool *x509.CertPool
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("alertmanager: the CA file: %w", err)
		}
		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("alertmanager: the CA file holds no certificate")
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // the token goes to the configured address, whatever the environment says
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	c := &Client{
		creds: creds,
		http: &http.Client{
			Timeout:   requestTimeout,
			Transport: &guard{base: tr},
			// An answer that redirects is an answer, not an instruction to send the token elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

func scrub(s string, token secret.Value) string {
	if t := token.Reveal(); t != "" {
		s = strings.ReplaceAll(s, t, "***")
	}
	return s
}

// ListAlerts returns the alerts that are active (not silenced, not inhibited). The answer is all or nothing: anything that is not a
// complete JSON array is an error.
func (c *Client) ListAlerts(ctx context.Context) ([]Alert, error) {
	base, token, err := c.creds()
	if err != nil {
		return nil, err
	}
	base = strings.TrimRight(base, "/")
	if base == "" {
		return nil, ErrNotConfigured
	}
	q := url.Values{"active": {"true"}, "silenced": {"false"}, "inhibited": {"false"}}
	target := base + "/api/v2/alerts?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, errors.New(scrub(err.Error(), token))
	}
	req.Header.Set("Accept", "application/json")
	if t := token.Reveal(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.logRequest(0, started)
		return nil, errors.New(scrub(err.Error(), token))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	c.logRequest(resp.StatusCode, started)
	if err != nil {
		return nil, errors.New(scrub(err.Error(), token))
	}
	if len(raw) > maxBody {
		return nil, ErrTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &APIError{Status: resp.StatusCode}
	}
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "[") {
		return nil, ErrNotAlerts
	}
	var all []Alert
	if err := json.Unmarshal([]byte(trimmed), &all); err != nil {
		// The decoder's message can quote the answer, which is text of another system: it is not shown.
		return nil, fmt.Errorf("%w: it is not valid JSON of alerts", ErrNotAlerts)
	}
	active := make([]Alert, 0, len(all))
	for _, a := range all {
		if a.Status.State == "active" {
			active = append(active, a)
		}
	}
	return active, nil
}

func (c *Client) logRequest(status int, started time.Time) {
	if c.log == nil {
		return
	}
	c.log.Debug("alertmanager request", "path", "/api/v2/alerts", "status", status, "took", time.Since(started).Round(time.Millisecond))
}
