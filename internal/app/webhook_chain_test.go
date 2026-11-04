package app_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func post(h http.Handler, path string) int {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestNoRouteExistsWithoutTheAddress(t *testing.T) {
	a := appWithEnv(t, nil)
	if a.WebhookHandler != nil {
		t.Fatal("a webhook handler exists although REMEDY_WEBHOOK_ADDR is empty")
	}
	if code := post(a.Handler, "/webhook/v1/grafana"); code != http.StatusNotFound {
		t.Errorf("the single-port handler answers %d", code)
	}
	split := appWithEnv(t, map[string]string{"REMEDY_INTERNAL_ADDR": ":8081"})
	for name, h := range map[string]http.Handler{"public": split.Handler, "internal": split.InternalHandler} {
		if code := post(h, "/webhook/v1/grafana"); code != http.StatusNotFound {
			t.Errorf("%s answers %d without the webhook address", name, code)
		}
	}
}

func TestTheWebhookListenerIsTheOnlyOneThatServesTheRoute(t *testing.T) {
	a := appWithEnv(t, map[string]string{"REMEDY_INTERNAL_ADDR": ":8081", "REMEDY_WEBHOOK_ADDR": ":8082"})
	if a.WebhookHandler == nil {
		t.Fatal("no webhook handler although the address is set")
	}
	for name, h := range map[string]http.Handler{"public": a.Handler, "internal": a.InternalHandler} {
		if code := post(h, "/webhook/v1/grafana"); code != http.StatusNotFound {
			t.Errorf("%s answers %d, want 404", name, code)
		}
	}
	if code := post(a.WebhookHandler, "/webhook/v1/grafana"); code != http.StatusUnauthorized {
		t.Errorf("the webhook listener answers %d for an unknown endpoint, want 401", code)
	}
	for _, p := range []string{"/api/me", "/runner/v1/claim", "/mcp"} {
		if code := post(a.WebhookHandler, p); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("the webhook listener answers %d for %s", code, p)
		}
	}
}
