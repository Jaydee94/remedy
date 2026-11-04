package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func TestTheWebhookListenerServesOnlyTheWebhookRouteAndHealth(t *testing.T) {
	hit := 0
	h := server.NewWebhook(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hit++; w.WriteHeader(http.StatusNoContent) }))
	for method, paths := range map[string][]string{
		http.MethodGet:  {"/api/me", "/api/incidents", "/runner/v1/claim", "/mcp", "/", "/webhook/v1/grafana", "/webhook/v1/"},
		http.MethodPost: {"/api/login", "/runner/v1/claim", "/mcp", "/webhook/v1/", "/webhook/v1/a/b", "/webhook"},
	} {
		for _, p := range paths {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, p, nil))
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d on the webhook listener", method, p, rec.Code)
			}
		}
	}
	if hit != 0 {
		t.Errorf("the webhook handler was reached %d times by a wrong path or method", hit)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhook/v1/grafana", nil))
	if rec.Code != http.StatusNoContent || hit != 1 {
		t.Errorf("POST /webhook/v1/grafana = %d, handler hit %d times", rec.Code, hit)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d", rec.Code)
	}
}

func TestNeitherThePublicNorTheInternalMuxServesTheWebhookRoute(t *testing.T) {
	d := server.Deps{Store: testutil.Store(t), Auth: auth.New(password), RunnerToken: runnerToken}
	pub, in := server.NewSplit(d)
	for name, h := range map[string]http.Handler{"public": pub, "internal": in, "single": server.New(d)} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhook/v1/grafana", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: POST /webhook/v1/grafana = %d, want 404", name, rec.Code)
		}
	}
}
