package github

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Jaydee94/remedy/internal/secret"
)

func TestTheTransportRefusesEverythingButGetAndHead(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(ts.Close)
	var buf bytes.Buffer
	c := audited(ts.Client(), "", slog.New(slog.NewTextHandler(&buf, nil)))

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		req, _ := http.NewRequest(method, ts.URL+"/repos/o/r/issues", strings.NewReader("{}"))
		if resp, err := c.Do(req); err == nil {
			_ = resp.Body.Close()
			t.Errorf("%s was sent", method)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("the server saw %d requests, want none", hits.Load())
	}
	if !strings.Contains(buf.String(), "refused") {
		t.Errorf("the refusals were not logged:\n%s", buf.String())
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, _ := http.NewRequest(method, ts.URL+"/repos/o/r", nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		_ = resp.Body.Close()
	}
	if hits.Load() != 2 {
		t.Fatalf("the server saw %d requests, want the GET and the HEAD", hits.Load())
	}
}

func TestNewGuardsTheAPIClientAndTheDownloadClient(t *testing.T) {
	clients := map[string]*Client{
		"default clients": New("https://api.github.com", secret.NewValue("t"), nil),
		"a given client":  New("https://api.github.com", secret.NewValue("t"), &http.Client{}),
	}
	for name, c := range clients {
		if _, ok := c.http.Transport.(*auditTransport); !ok {
			t.Errorf("%s: the API client is not guarded", name)
		}
		if _, ok := c.stream.Transport.(*auditTransport); !ok {
			t.Errorf("%s: the download client is not guarded", name)
		}
	}
}
