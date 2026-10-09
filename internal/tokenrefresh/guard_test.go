package tokenrefresh

import (
	"net/http"
	"testing"
)

func TestTheGuardAllowsExactlyTwoRequests(t *testing.T) {
	g := &guard{mintPath: "/api/v1/namespaces/n/serviceaccounts/a/token", secretPath: "/api/v1/namespaces/n/secrets/s"}
	allowed := map[string]bool{
		"POST /api/v1/namespaces/n/serviceaccounts/a/token": true,
		"PATCH /api/v1/namespaces/n/secrets/s":              true,
	}
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"} {
		for _, path := range []string{
			"/api/v1/namespaces/n/serviceaccounts/a/token",
			"/api/v1/namespaces/n/secrets/s",
			"/api/v1/namespaces/n/secrets/other",
			"/api/v1/namespaces/other/secrets/s",
			"/api/v1/namespaces/n/serviceaccounts/a",
			"/api/v1/namespaces/n/secrets",
			"/api/v1/secrets",
		} {
			if got, want := g.allows(method, path), allowed[method+" "+path]; got != want {
				t.Errorf("%s %s: allows = %v, want %v", method, path, got, want)
			}
		}
	}
	// And the transport itself refuses before anything is sent.
	req, _ := http.NewRequest(http.MethodDelete, "http://127.0.0.1:1/api/v1/namespaces/n/secrets/s", nil)
	if _, err := g.RoundTrip(req); err == nil {
		t.Fatal("RoundTrip must refuse a request that is not allowed")
	}
}
