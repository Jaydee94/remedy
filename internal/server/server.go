// Package server hosts the Remedy control plane HTTP surface: the admin API used by the UI
// and the runner API used by remedy-runner.
package server

import (
	"encoding/json"
	"io/fs"
	"net"
	"net/http"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

type Deps struct {
	Store       *store.Store
	Auth        *auth.Auth
	RunnerToken string
	Web         fs.FS // optional: the built UI, served for every non-API path

	// Key seals the GitHub token. NewGitHub builds a client for a token; when it is nil the GitHub
	// routes are not registered.
	Key       secret.Key
	NewGitHub func(token secret.Value) GitHub
}

type srv struct {
	d   Deps
	hub *hub
}

func New(d Deps) http.Handler {
	s := &srv{d: d, hub: newHub()}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.session(s.logout))
	mux.HandleFunc("GET /api/me", s.session(s.me))
	mux.HandleFunc("POST /api/runs", s.session(s.createRun))
	mux.HandleFunc("GET /api/runs", s.session(s.listRuns))
	mux.HandleFunc("GET /api/runs/{id}", s.session(s.getRun))
	mux.HandleFunc("GET /api/runs/{id}/events", s.session(s.streamEvents))

	mux.HandleFunc("POST /runner/v1/claim", s.runner(s.claim))
	mux.HandleFunc("POST /runner/v1/runs/{id}/events", s.runner(s.postEvent))
	mux.HandleFunc("POST /runner/v1/runs/{id}/finish", s.runner(s.finish))

	if d.NewGitHub != nil {
		mux.HandleFunc("GET /api/github/connection", s.session(s.getConnection))
		mux.HandleFunc("PUT /api/github/connection", s.session(s.putConnection))
		mux.HandleFunc("POST /api/github/connection/check", s.session(s.checkConnection))
		mux.HandleFunc("DELETE /api/github/connection", s.session(s.deleteConnection))
		mux.HandleFunc("GET /api/repos", s.session(s.listRepos))
		mux.HandleFunc("POST /api/repos", s.session(s.addRepo))
		mux.HandleFunc("PATCH /api/repos/{id}", s.session(s.patchRepo))
		mux.HandleFunc("DELETE /api/repos/{id}", s.session(s.deleteRepo))
	}

	if d.Web != nil {
		mux.Handle("/", spa(d.Web))
	}
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// clientIP is the TCP peer address. Proxy headers are deliberately not trusted.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
