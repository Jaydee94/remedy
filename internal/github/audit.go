package github

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Option changes how New builds a Client.
type Option func(*options)

type options struct{ log *slog.Logger }

// WithLog makes the client log every request at debug level: the method, the host, the status and the
// duration, and the path for the API host. It never logs a query (a redirect to a download carries a signed
// address in it), a header, or the path of another host.
func WithLog(log *slog.Logger) Option { return func(o *options) { o.log = log } }

// auditTransport is what every request of a Client goes through. It is the second line of defence behind
// the read-only methods of Client: whatever calls it, nothing but GET and HEAD leaves the process.
type auditTransport struct {
	base    http.RoundTripper
	apiHost string
	log     *slog.Logger // nil when nothing is logged
}

func (t *auditTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		if t.log != nil {
			t.log.Error("github request refused: the client is read-only", "method", req.Method, "host", req.URL.Host)
		}
		return nil, fmt.Errorf("github: refusing to send %s: the client is read-only", req.Method)
	}

	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	if t.log != nil {
		attrs := []any{"method", req.Method, "host", req.URL.Host}
		if req.URL.Host == t.apiHost {
			attrs = append(attrs, "path", req.URL.Path)
		}
		if err != nil {
			attrs = append(attrs, "failed", true) // the error text may contain the address
		} else {
			attrs = append(attrs, "status", resp.StatusCode)
		}
		attrs = append(attrs, "took", time.Since(started).Round(time.Millisecond))
		t.log.Debug("github request", attrs...)
	}
	return resp, err
}

// audited returns a copy of c whose requests go through an auditTransport.
func audited(c *http.Client, apiHost string, log *slog.Logger) *http.Client {
	guarded := *c
	base := guarded.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	guarded.Transport = &auditTransport{base: base, apiHost: apiHost, log: log}
	return &guarded
}
