// Package server hosts the Remedy control plane HTTP surface: the admin API used by the UI
// and the runner API used by remedy-runner.
package server

import (
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/responder"
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

	// Incidents changes incidents on behalf of the UI. When it is nil the incident routes are not
	// registered.
	Incidents *incident.Engine

	// Responder diagnoses incidents. When it is nil there are no diagnose, snapshot and limits routes.
	Responder    *responder.Responder
	PollInterval time.Duration // only shown by the limits endpoint

	// ActivityInterval is how often the activity stream looks for new entries. Zero means one second.
	ActivityInterval time.Duration

	// Gatekeeper serves the MCP tools of agents at /mcp and decides approvals. When it is nil there are neither, and
	// a run cannot be created with tools.
	Gatekeeper *gatekeeper.Gatekeeper

	// Cluster says what can be done in a cluster. The zero value is no cluster.
	Cluster Cluster
}

type srv struct {
	d   Deps
	hub *hub
}

// New returns one handler that serves everything: the UI, the admin API, the runner API and /mcp.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	(&srv{d: d, hub: newHub()}).routes(mux, mux)
	return mux
}

// NewSplit returns two handlers for two ports. public serves the UI, /api and /healthz. internal serves /runner/v1 and
// /mcp, which only the runner and the agent CLI next to it call, and /healthz. An Ingress that points at the public
// port cannot reach the runner token's routes or the run tokens' route.
func NewSplit(d Deps) (public, internal http.Handler) {
	pub, in := http.NewServeMux(), http.NewServeMux()
	(&srv{d: d, hub: newHub()}).routes(pub, in)
	return pub, in
}

// routes registers every route. public gets the UI and the admin API, internal the runner API and /mcp. They may be
// the same mux: that is the single-port server.
func (s *srv) routes(public, internal *http.ServeMux) {
	d := s.d
	health := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	public.HandleFunc("GET /healthz", health)
	if internal != public {
		internal.HandleFunc("GET /healthz", health)
	}

	public.HandleFunc("POST /api/login", s.login)
	public.HandleFunc("POST /api/logout", s.session(s.logout))
	public.HandleFunc("GET /api/me", s.session(s.me))
	public.HandleFunc("POST /api/runs", s.session(s.createRun))
	public.HandleFunc("GET /api/capabilities", s.session(s.capabilities))
	public.HandleFunc("GET /api/runs", s.session(s.listRuns))
	public.HandleFunc("GET /api/runs/{id}", s.session(s.getRun))
	public.HandleFunc("GET /api/runs/{id}/events", s.session(s.streamEvents))
	public.HandleFunc("GET /api/runs/{id}/tool-calls", s.session(s.listToolCalls))
	public.HandleFunc("POST /api/runs/{id}/cancel", s.session(s.cancelRun))
	if d.Gatekeeper != nil {
		// The gatekeeper authenticates by the run token itself: this is neither an admin nor a runner route.
		internal.Handle("/mcp", d.Gatekeeper)
		public.HandleFunc("GET /api/approvals", s.session(s.listApprovals))
		public.HandleFunc("POST /api/approvals/{id}/approve", s.session(s.decide(true)))
		public.HandleFunc("POST /api/approvals/{id}/deny", s.session(s.decide(false)))
	}
	public.HandleFunc("GET /api/activity", s.session(s.listActivity))
	public.HandleFunc("GET /api/activity/stream", s.session(s.streamActivity))

	internal.HandleFunc("POST /runner/v1/claim", s.runner(s.claim))
	internal.HandleFunc("POST /runner/v1/runs/{id}/events", s.runner(s.postEvent))
	internal.HandleFunc("POST /runner/v1/runs/{id}/finish", s.runner(s.finish))
	internal.HandleFunc("POST /runner/v1/runs/{id}/heartbeat", s.runner(s.heartbeat))

	if d.NewGitHub != nil {
		public.HandleFunc("GET /api/github/connection", s.session(s.getConnection))
		public.HandleFunc("PUT /api/github/connection", s.session(s.putConnection))
		public.HandleFunc("POST /api/github/connection/check", s.session(s.checkConnection))
		public.HandleFunc("DELETE /api/github/connection", s.session(s.deleteConnection))
		public.HandleFunc("GET /api/repos", s.session(s.listRepos))
		public.HandleFunc("POST /api/repos", s.session(s.addRepo))
		public.HandleFunc("PATCH /api/repos/{id}", s.session(s.patchRepo))
		public.HandleFunc("DELETE /api/repos/{id}", s.session(s.deleteRepo))
	}

	if d.Incidents != nil {
		public.HandleFunc("GET /api/incidents", s.session(s.listIncidents))
		public.HandleFunc("GET /api/incidents/{id}", s.session(s.getIncident))
		public.HandleFunc("POST /api/incidents/{id}/ignore", s.session(s.ignoreIncident))
		public.HandleFunc("POST /api/incidents/{id}/unignore", s.session(s.unignoreIncident))
	}

	if d.Responder != nil {
		public.HandleFunc("POST /api/incidents/{id}/diagnose", s.session(s.diagnoseIncident))
		public.HandleFunc("GET /api/limits", s.session(s.getLimits))
		internal.HandleFunc("GET /runner/v1/runs/{id}/snapshot", s.runner(s.snapshot))
	}

	if d.Web != nil {
		public.Handle("/", spa(d.Web))
	}
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
