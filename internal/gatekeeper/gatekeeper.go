// Package gatekeeper is the MCP server of the control plane. Agents get no raw shell and no credentials: everything
// they do goes through its tools. Read tools run at once; mutating tools wait for the maintainer's approval; every
// call is one row in the audit log.
package gatekeeper

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

const (
	maxRequestBytes = 256 << 10
	// defaultProtocol is what initialize answers when the client names none.
	defaultProtocol = "2025-03-26"
)

type Config struct {
	Store *store.Store
	Tools []Tool
	// ProgressInterval is how often a waiting call sends a progress notification. Default 15 seconds.
	ProgressInterval time.Duration
	// Grace is how long a call that nobody waits for stays open for a replay to attach to it. Default 30 seconds.
	Grace time.Duration
	Log   *slog.Logger
}

type Gatekeeper struct {
	store    *store.Store
	tools    []Tool
	byName   map[string]Tool
	progress time.Duration
	grace    time.Duration
	log      *slog.Logger

	mu      sync.Mutex
	waiters map[int64]*waiterSet // by tool call ID
}

// New builds a gatekeeper. It panics for a registry that cannot work (a duplicate or malformed tool): that is a
// programming error, not something to handle at run time.
func New(c Config) *Gatekeeper {
	g := &Gatekeeper{
		store: c.Store, tools: c.Tools, byName: map[string]Tool{},
		progress: c.ProgressInterval, grace: c.Grace, log: c.Log,
		waiters: map[int64]*waiterSet{},
	}
	if g.progress <= 0 {
		g.progress = 15 * time.Second
	}
	if g.grace <= 0 {
		g.grace = 30 * time.Second
	}
	if g.log == nil {
		g.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	for _, t := range c.Tools {
		if err := validateTool(t); err != nil {
			panic("gatekeeper: " + err.Error())
		}
		if _, dup := g.byName[t.Name]; dup {
			panic("gatekeeper: tool " + t.Name + " is registered twice")
		}
		g.byName[t.Name] = t
	}
	return g
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (g *Gatekeeper) authenticate(r *http.Request) (run.Run, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return run.Run{}, store.ErrNotFound
	}
	return g.store.RunForToken(r.Context(), token)
}

// ServeHTTP is the MCP endpoint: Streamable HTTP, POST for messages. GET (a server-to-client stream) is not offered,
// and DELETE (ending a session) has nothing to end.
func (g *Gatekeeper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	default:
		w.Header().Set("Allow", "POST, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	r0, err := g.authenticate(r)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired run token"})
		return
	}
	if err != nil {
		g.log.Error("run token lookup failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not check the run token"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, failure(nil, codeInvalidRequest, "the request is too large or unreadable"))
		return
	}
	if t := strings.TrimSpace(string(body)); strings.HasPrefix(t, "[") {
		writeJSON(w, http.StatusBadRequest, failure(nil, codeInvalidRequest, "batches are not supported"))
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, failure(nil, codeParse, "the request is not valid JSON"))
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeJSON(w, http.StatusBadRequest, failure(req.ID, codeInvalidRequest, "not a JSON-RPC 2.0 request"))
		return
	}
	if req.notification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = defaultProtocol
		}
		writeJSON(w, http.StatusOK, result(req.ID, map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "remedy", "version": "1"},
		}))
	case "ping":
		writeJSON(w, http.StatusOK, result(req.ID, map[string]any{}))
	case "tools/list":
		list := make([]map[string]any, 0, len(g.tools))
		for _, t := range g.tools {
			if !t.offeredTo(r0) {
				continue
			}
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
		}
		writeJSON(w, http.StatusOK, result(req.ID, map[string]any{"tools": list}))
	case "tools/call":
		g.callTool(w, r, r0, req)
	default:
		writeJSON(w, http.StatusOK, failure(req.ID, codeMethodNotFound, "method not found: "+req.Method))
	}
}
