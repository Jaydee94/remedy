package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Jaydee94/remedy/internal/server"
)

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	server.New().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Body.String(), "{\"status\":\"ok\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}
