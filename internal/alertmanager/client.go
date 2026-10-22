// Package alertmanager is Remedy's read-only client of an Alertmanager. It asks for alerts and silences and nothing else: every
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
	"unicode"

	"github.com/Jaydee94/remedy/internal/secret"
)

// maxBody bounds an answer: a bigger one is an error, never a partial list.
const maxBody = 8 << 20

// maxFilters is the most matchers one request carries.
const maxFilters = 10

// requestTimeout is the time one request may take. It is a variable only so that a test does not wait for it.
var requestTimeout = 15 * time.Second

var (
	// ErrNotConfigured means no address is set. ErrTooLarge means the answer is bigger than the client reads. ErrNotAlerts means
	// the answer is not a JSON array of alerts (or of silences). ErrBadFilter means a filter matcher was refused before anything
	// was sent: it is empty, it holds a control character, or there are more than 10.
	ErrNotConfigured = errors.New("alertmanager: no address is set")
	ErrTooLarge      = errors.New("alertmanager: the answer is too large")
	ErrNotAlerts     = errors.New("alertmanager: the answer is not a JSON array of alerts")
	ErrBadFilter     = errors.New("alertmanager: a filter is empty or has a control character, or there are more than 10")
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
		State       string   `json:"state"`
		SilencedBy  []string `json:"silencedBy"`
		InhibitedBy []string `json:"inhibitedBy"`
	} `json:"status"`
}

// Matcher is one matcher of a silence.
type Matcher struct {
	Name, Value      string
	IsRegex, IsEqual bool
}

// Silence is one silence of the v2 API. State is "active", "pending" or "expired".
type Silence struct {
	ID                 string
	Matchers           []Matcher
	StartsAt, EndsAt   time.Time
	CreatedBy, Comment string
	State              string
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

// Client asks an Alertmanager for its alerts and silences.
type Client struct {
	creds Credentials
	http  *http.Client
	log   *slog.Logger
}

// New builds a client. caFile is a PEM bundle for an https address; empty means the system's roots. creds is only called by
// the List methods.
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
	q := url.Values{"active": {"true"}, "silenced": {"false"}, "inhibited": {"false"}}
	all, err := c.alerts(ctx, q)
	if err != nil {
		return nil, err
	}
	active := make([]Alert, 0, len(all))
	for _, a := range all {
		if a.Status.State == "active" {
			active = append(active, a)
		}
	}
	return active, nil
}

// ListAlertsFiltered returns the alerts that match every filter (Alertmanager matcher strings such as `alertname=~"Node.*"`, passed
// on as given and URL-encoded). With activeOnly it asks for the alerts that are neither silenced nor inhibited; without it the
// answer also holds silenced and inhibited alerts, and their Status says by what. At most 10 filters; a filter that is empty or has
// a control character is refused with ErrBadFilter before anything is sent. Like ListAlerts, the answer is all or nothing.
func (c *Client) ListAlertsFiltered(ctx context.Context, filters []string, activeOnly bool) ([]Alert, error) {
	if len(filters) > maxFilters {
		return nil, ErrBadFilter
	}
	q := url.Values{}
	for _, f := range filters {
		if f == "" || strings.IndexFunc(f, unicode.IsControl) >= 0 {
			return nil, ErrBadFilter
		}
		q.Add("filter", f)
	}
	if activeOnly {
		q.Set("active", "true")
		q.Set("silenced", "false")
		q.Set("inhibited", "false")
	}
	return c.alerts(ctx, q)
}

func (c *Client) alerts(ctx context.Context, q url.Values) ([]Alert, error) {
	raw, err := c.get(ctx, "/api/v2/alerts", q)
	if err != nil {
		return nil, err
	}
	var all []Alert
	if err := json.Unmarshal(raw, &all); err != nil {
		// The decoder's message can quote the answer, which is text of another system: it is not shown.
		return nil, fmt.Errorf("%w: it is not valid JSON of alerts", ErrNotAlerts)
	}
	return all, nil
}

// ListSilences returns the silences of the Alertmanager in every state. The answer is all or nothing, like that of ListAlerts.
func (c *Client) ListSilences(ctx context.Context) ([]Silence, error) {
	raw, err := c.get(ctx, "/api/v2/silences", nil)
	if err != nil {
		return nil, err
	}
	var in []struct {
		ID       string `json:"id"`
		Matchers []struct {
			Name    string `json:"name"`
			Value   string `json:"value"`
			IsRegex bool   `json:"isRegex"`
			IsEqual *bool  `json:"isEqual"`
		} `json:"matchers"`
		StartsAt  time.Time `json:"startsAt"`
		EndsAt    time.Time `json:"endsAt"`
		CreatedBy string    `json:"createdBy"`
		Comment   string    `json:"comment"`
		Status    struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("%w: it is not valid JSON of silences", ErrNotAlerts)
	}
	out := make([]Silence, 0, len(in))
	for _, s := range in {
		sl := Silence{ID: s.ID, StartsAt: s.StartsAt, EndsAt: s.EndsAt, CreatedBy: s.CreatedBy, Comment: s.Comment, State: s.Status.State}
		for _, m := range s.Matchers {
			// An Alertmanager before 0.22 sends no isEqual: the matcher is an equality.
			sl.Matchers = append(sl.Matchers, Matcher{Name: m.Name, Value: m.Value, IsRegex: m.IsRegex, IsEqual: m.IsEqual == nil || *m.IsEqual})
		}
		out = append(out, sl)
	}
	return out, nil
}

// get sends one GET and returns the body of a successful answer that is a JSON array. Anything else is an error.
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	base, token, err := c.creds()
	if err != nil {
		return nil, err
	}
	base = strings.TrimRight(base, "/")
	if base == "" {
		return nil, ErrNotConfigured
	}
	target := base + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
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
		c.logRequest(path, 0, started)
		return nil, errors.New(scrub(err.Error(), token))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	c.logRequest(path, resp.StatusCode, started)
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
	return []byte(trimmed), nil
}

func (c *Client) logRequest(path string, status int, started time.Time) {
	if c.log == nil {
		return
	}
	c.log.Debug("alertmanager request", "path", path, "status", status, "took", time.Since(started).Round(time.Millisecond))
}
