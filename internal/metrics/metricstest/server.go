// Package metricstest is a fake metrics store (Prometheus or VictoriaMetrics HTTP API) for tests: it serves the instant query, the
// range query and the build info from canned JSON, under any base path, and records every request.
package metricstest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Request is what the fake saw.
type Request struct{ Method, Path, Query, Auth string }

// Canned answers: an empty vector, an empty matrix and a build info.
const (
	EmptyVector = `{"status":"success","data":{"resultType":"vector","result":[]}}`
	EmptyMatrix = `{"status":"success","data":{"resultType":"matrix","result":[]}}`
	BuildInfo   = `{"status":"success","data":{"version":"2.53.0","revision":"abc","branch":"main"}}`
)

// Server is a fake metrics store. With a token it answers 401 to a request that does not carry it.
type Server struct {
	URL string

	mu     sync.Mutex
	token  string
	status int // 0: the canned answers
	body   []byte
	reqs   []Request
}

// New starts a fake with the canned answers.
func New(t testing.TB, token string) *Server {
	t.Helper()
	s := &Server{token: token}
	ts := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(ts.Close)
	s.URL = ts.URL
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization")})
	status, body, token := s.status, s.body, s.token
	s.mu.Unlock()
	if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
		http.Error(w, `{"status":"error","errorType":"unauthorized","error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	canned := ""
	switch {
	case strings.HasSuffix(r.URL.Path, "/api/v1/query_range"):
		canned = EmptyMatrix
	case strings.HasSuffix(r.URL.Path, "/api/v1/query"):
		canned = EmptyVector
	case strings.HasSuffix(r.URL.Path, "/api/v1/status/buildinfo"):
		canned = BuildInfo
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if status == 0 {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(canned))
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// SetRaw makes the fake answer every endpoint with a status and exactly this body.
func (s *Server) SetRaw(status int, body string) { s.SetBody(status, []byte(body)) }

// SetBody is SetRaw for bytes.
func (s *Server) SetBody(status int, body []byte) {
	s.mu.Lock()
	s.status, s.body = status, body
	s.mu.Unlock()
}

// Requests returns what the fake has seen so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}
