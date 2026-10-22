// Package metrics is Remedy's read-only client of a metrics store that speaks the Prometheus HTTP API (Prometheus, VictoriaMetrics).
// It asks the instant query, the range query and the build info and nothing else: the transport refuses every method but GET and
// HEAD before it is sent, redirects are not followed and no proxy is used. The address (it may carry a base path) and the token are
// asked for at every request, because the Setup page can change them while the control plane runs.
package metrics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	// maxBody bounds what is read of an answer. A bigger answer is cut: the series that were read whole are kept and the result says
	// so (Result.Truncated).
	maxBody = 4 << 20
	// maxMessage bounds the message of a refused query, which is text of another system.
	maxMessage = 200
)

// requestTimeout is the time one request may take. It is a variable only so that a test does not wait for it.
var requestTimeout = 10 * time.Second

var (
	// ErrNotConfigured means no address is set. ErrNotMetrics means the answer is not the JSON of a query result.
	ErrNotConfigured = errors.New("metrics: no address is set")
	ErrNotMetrics    = errors.New("metrics: the answer is not the JSON of a query result")
	// ErrBadRequest means the arguments cannot make a query; nothing was sent.
	ErrBadRequest = errors.New("metrics: the query needs an expression and, for a range, a step above zero and an end after the start")
)

// APIError is an answer that is not a success and says nothing else. The body is not shown: it is text of another system.
type APIError struct{ Status int }

func (e *APIError) Error() string { return fmt.Sprintf("metrics: HTTP %d", e.Status) }

// QueryError is the store refusing a query (a PromQL syntax error, a query that is too expensive). Message is the store's own text,
// clipped to one line of at most 200 characters, with the token removed: the caller treats it as data.
type QueryError struct{ Message string }

func (e *QueryError) Error() string { return "metrics: the query was refused: " + e.Message }

// Result is the answer to a query. Type is vector, matrix or scalar. Truncated is set when the client cut the answer at its byte
// limit: the series after the cut are missing.
type Result struct {
	Type      string
	Series    []Series
	Truncated bool
}

// Series is one series with its labels. A vector has one sample per series, a scalar one series without labels.
type Series struct {
	Labels  map[string]string
	Samples []Sample
}

// Sample is one value at one time. Value can be NaN or an infinity.
type Sample struct {
	At    time.Time
	Value float64
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
		return nil, fmt.Errorf("metrics: refusing to send %s: this client only reads", req.Method)
	}
	return g.base.RoundTrip(req)
}

// Client asks a metrics store for query results.
type Client struct {
	creds Credentials
	http  *http.Client
	log   *slog.Logger
}

// New builds a client. caFile is a PEM bundle for an https address; empty means the system's roots.
func New(creds Credentials, caFile string, opts ...Option) (*Client, error) {
	var pool *x509.CertPool
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("metrics: the CA file: %w", err)
		}
		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("metrics: the CA file holds no certificate")
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

// unixSeconds writes a time the way the API reads it: seconds with milliseconds.
func unixSeconds(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixMilli())/1000, 'f', 3, 64)
}

// Query runs an instant query. A zero at leaves the evaluation time to the store.
func (c *Client) Query(ctx context.Context, expr string, at time.Time) (Result, error) {
	if strings.TrimSpace(expr) == "" {
		return Result{}, ErrBadRequest
	}
	q := url.Values{"query": {expr}}
	if !at.IsZero() {
		q.Set("time", unixSeconds(at))
	}
	return c.result(ctx, "/api/v1/query", q)
}

// QueryRange runs a range query.
func (c *Client) QueryRange(ctx context.Context, expr string, start, end time.Time, step time.Duration) (Result, error) {
	if strings.TrimSpace(expr) == "" || step <= 0 || end.Before(start) {
		return Result{}, ErrBadRequest
	}
	q := url.Values{
		"query": {expr}, "start": {unixSeconds(start)}, "end": {unixSeconds(end)},
		"step": {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)},
	}
	return c.result(ctx, "/api/v1/query_range", q)
}

// Ping asks for the build info. Any success is an answer from a store that this token may use.
func (c *Client) Ping(ctx context.Context) error {
	resp, _, err := c.get(ctx, "/api/v1/status/buildinfo", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Status: resp.StatusCode}
	}
	return nil
}

// get sends one GET. The caller closes the body.
func (c *Client) get(ctx context.Context, path string, q url.Values) (*http.Response, secret.Value, error) {
	base, token, err := c.creds()
	if err != nil {
		return nil, token, err
	}
	base = strings.TrimRight(base, "/")
	if base == "" {
		return nil, token, ErrNotConfigured
	}
	target := base + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, token, errors.New(scrub(err.Error(), token))
	}
	req.Header.Set("Accept", "application/json")
	if t := token.Reveal(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.logRequest(path, 0, started)
		return nil, token, errors.New(scrub(err.Error(), token))
	}
	c.logRequest(path, resp.StatusCode, started)
	return resp, token, nil
}

func (c *Client) result(ctx context.Context, path string, q url.Values) (Result, error) {
	resp, token, err := c.get(ctx, path, q)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	cr := &countingReader{r: io.LimitReader(resp.Body, maxBody)}
	res, qerr, err := parseAnswer(cr, cr.atLimit)
	if qerr != nil {
		qerr.Message = clip(scrub(qerr.Message, token))
		return Result{}, qerr
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Result{}, &APIError{Status: resp.StatusCode}
	}
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// clip makes the store's message one short line.
func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxMessage {
		s = string([]rune(s)[:maxMessage])
	}
	return s
}

func (c *Client) logRequest(path string, status int, started time.Time) {
	if c.log == nil {
		return
	}
	c.log.Debug("metrics request", "path", path, "status", status, "took", time.Since(started).Round(time.Millisecond))
}

// countingReader counts what it hands out, so that the parser can tell a cut-off answer from a malformed one.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func (c *countingReader) atLimit() bool { return c.n >= maxBody }

// parseAnswer reads the JSON of a query result as a stream, so that an answer cut at the byte limit still gives the series that
// came whole. A store's refusal is returned as qerr (with its message not yet clipped); anything else that is wrong is
// ErrNotMetrics, which quotes nothing.
func parseAnswer(r io.Reader, atLimit func() bool) (res Result, qerr *QueryError, err error) {
	dec := json.NewDecoder(r)
	bad := func() (Result, *QueryError, error) { return Result{}, nil, ErrNotMetrics }
	if !isDelim(dec, '{') {
		return bad()
	}
	var status, message string
	for dec.More() {
		key, ok := stringToken(dec)
		if !ok {
			return bad()
		}
		switch key {
		case "status":
			if dec.Decode(&status) != nil {
				return bad()
			}
		case "error":
			if dec.Decode(&message) != nil {
				return bad()
			}
		case "data":
			if status != "success" {
				// the store says the status first; an answer that does not is not one this client knows
				return bad()
			}
			d, ok := parseData(dec, atLimit)
			if !ok {
				return bad()
			}
			return d, nil, nil
		default:
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return bad()
			}
		}
	}
	if status == "error" {
		return Result{}, &QueryError{Message: message}, nil
	}
	return bad()
}

// parseData reads the object after "data" up to the end of its result. It returns false when the answer cannot be a result.
func parseData(dec *json.Decoder, atLimit func() bool) (Result, bool) {
	var res Result
	if !isDelim(dec, '{') {
		return Result{}, false
	}
	for dec.More() {
		key, ok := stringToken(dec)
		if !ok {
			return Result{}, false
		}
		switch key {
		case "resultType":
			if dec.Decode(&res.Type) != nil {
				return Result{}, false
			}
		case "result":
			switch res.Type {
			case "vector", "matrix":
				return readSeries(dec, res, atLimit)
			case "scalar":
				var pair [2]json.RawMessage
				if dec.Decode(&pair) != nil {
					return Result{}, false
				}
				s, ok := sample(pair)
				if !ok {
					return Result{}, false
				}
				res.Series = []Series{{Labels: map[string]string{}, Samples: []Sample{s}}}
				return res, true
			default:
				return Result{}, false // a string result, or a result that came before its type
			}
		default:
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return Result{}, false
			}
		}
	}
	return Result{}, false
}

// readSeries reads the array of a vector or a matrix. When the byte limit cuts the answer inside it, the series that were whole are
// kept.
func readSeries(dec *json.Decoder, res Result, atLimit func() bool) (Result, bool) {
	cut := func() (Result, bool) {
		if atLimit() {
			res.Truncated = true
			return res, true
		}
		return Result{}, false
	}
	if !isDelim(dec, '[') {
		return cut()
	}
	res.Series = []Series{}
	for dec.More() {
		var raw struct {
			Metric map[string]string    `json:"metric"`
			Value  *[2]json.RawMessage  `json:"value"`
			Values [][2]json.RawMessage `json:"values"`
		}
		if err := dec.Decode(&raw); err != nil {
			return cut()
		}
		s := Series{Labels: raw.Metric}
		if s.Labels == nil {
			s.Labels = map[string]string{}
		}
		if raw.Value != nil {
			smp, ok := sample(*raw.Value)
			if !ok {
				return Result{}, false
			}
			s.Samples = append(s.Samples, smp)
		}
		for _, p := range raw.Values {
			smp, ok := sample(p)
			if !ok {
				return Result{}, false
			}
			s.Samples = append(s.Samples, smp)
		}
		res.Series = append(res.Series, s)
	}
	return res, true
}

// sample reads [unix seconds, "value"].
func sample(p [2]json.RawMessage) (Sample, bool) {
	var ts float64
	var v string
	if json.Unmarshal(p[0], &ts) != nil || json.Unmarshal(p[1], &v) != nil {
		return Sample{}, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return Sample{}, false
	}
	sec, frac := math.Modf(ts)
	return Sample{At: time.Unix(int64(sec), int64(math.Round(frac*1e3))*1e6), Value: f}, true
}

func isDelim(dec *json.Decoder, want json.Delim) bool {
	tok, err := dec.Token()
	d, ok := tok.(json.Delim)
	return err == nil && ok && d == want
}

func stringToken(dec *json.Decoder) (string, bool) {
	tok, err := dec.Token()
	s, ok := tok.(string)
	return s, err == nil && ok
}
