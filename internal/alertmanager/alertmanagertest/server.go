// Package alertmanagertest is a fake Alertmanager for tests: it answers GET /api/v2/alerts and GET /api/v2/silences with what a test
// sets and records every request. testdata/home-server.json is an answer shaped like the one of the home server.
package alertmanagertest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Request is what the fake saw.
type Request struct{ Method, Path, Query, Auth string }

// Server is a fake Alertmanager. With a token it answers 401 to a request that does not carry it.
type Server struct {
	URL string

	mu     sync.Mutex
	token  string
	status int
	body   []byte
	reqs   []Request

	silenceStatus int
	silenceBody   []byte
}

// New starts a fake that answers an empty list.
func New(t testing.TB, token string) *Server {
	t.Helper()
	s := &Server{token: token, status: http.StatusOK, body: []byte("[]"), silenceStatus: http.StatusOK, silenceBody: []byte("[]")}
	ts := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(ts.Close)
	s.URL = ts.URL
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization")})
	status, body, token := s.status, s.body, s.token
	if r.URL.Path == "/api/v2/silences" {
		status, body = s.silenceStatus, s.silenceBody
	}
	s.mu.Unlock()
	if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if r.URL.Path != "/api/v2/alerts" && r.URL.Path != "/api/v2/silences" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// SetAlerts makes the fake answer these alerts with status 200.
func (s *Server) SetAlerts(alerts ...map[string]any) {
	if alerts == nil {
		alerts = []map[string]any{}
	}
	b, _ := json.Marshal(alerts)
	s.SetBody(b)
}

// SetBody makes the fake answer 200 with exactly these bytes.
func (s *Server) SetBody(b []byte) {
	s.mu.Lock()
	s.status, s.body = http.StatusOK, b
	s.mu.Unlock()
}

// SetRaw makes the fake answer with a status and a body.
func (s *Server) SetRaw(status int, body string) {
	s.mu.Lock()
	s.status, s.body = status, []byte(body)
	s.mu.Unlock()
}

// SetSilences makes the fake answer these silences with status 200.
func (s *Server) SetSilences(silences ...map[string]any) {
	if silences == nil {
		silences = []map[string]any{}
	}
	b, _ := json.Marshal(silences)
	s.SetSilencesBody(string(b))
}

// SetSilencesBody makes the fake answer 200 to /api/v2/silences with exactly this text.
func (s *Server) SetSilencesBody(body string) { s.SetSilencesRaw(http.StatusOK, body) }

// SetSilencesRaw makes the fake answer /api/v2/silences with a status and a body.
func (s *Server) SetSilencesRaw(status int, body string) {
	s.mu.Lock()
	s.silenceStatus, s.silenceBody = status, []byte(body)
	s.mu.Unlock()
}

// Requests returns what the fake has seen so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

// Alert builds one alert of the v2 API in the state active.
func Alert(fingerprint, name string, labels map[string]string, startsAt time.Time) map[string]any {
	l := map[string]string{"alertname": name}
	for k, v := range labels {
		l[k] = v
	}
	return map[string]any{
		"fingerprint": fingerprint, "labels": l, "annotations": map[string]string{"summary": name + " is firing"},
		"startsAt": startsAt.UTC().Format(time.RFC3339Nano), "endsAt": startsAt.Add(time.Hour).UTC().Format(time.RFC3339Nano),
		"generatorURL": "http://prometheus.example/graph?g0.expr=up", "receivers": []map[string]string{{"name": "blackhole"}},
		"status": map[string]any{"state": "active", "silencedBy": []string{}, "inhibitedBy": []string{}},
	}
}

// Silence builds one silence of the v2 API in the given state ("active", "pending" or "expired"). A matcher is a map with the members
// name, value, isRegex and isEqual.
func Silence(id, state string, startsAt, endsAt time.Time, createdBy, comment string, matchers ...map[string]any) map[string]any {
	if matchers == nil {
		matchers = []map[string]any{}
	}
	return map[string]any{
		"id": id, "matchers": matchers, "createdBy": createdBy, "comment": comment,
		"startsAt":  startsAt.UTC().Format(time.RFC3339Nano),
		"endsAt":    endsAt.UTC().Format(time.RFC3339Nano),
		"updatedAt": startsAt.UTC().Format(time.RFC3339Nano),
		"status":    map[string]any{"state": state},
	}
}
