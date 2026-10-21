package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

const maxPromptBytes = 20_000

func (s *srv) createRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider   string  `json:"provider"`
		Prompt     string  `json:"prompt"`
		Tools      bool    `json:"tools"`
		Cluster    bool    `json:"cluster"`
		IncidentID *int64  `json:"incidentId"`
		Hosts      []int64 `json:"hosts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Provider == "" {
		req.Provider = "claude"
	}
	// Phase 4 replaces this with a provider registry.
	if req.Provider != "claude" {
		writeErr(w, http.StatusBadRequest, "unknown provider")
		return
	}
	if req.Prompt == "" || len(req.Prompt) > maxPromptBytes {
		writeErr(w, http.StatusBadRequest, "prompt must be 1 to 20000 bytes")
		return
	}
	if req.Tools && s.d.Gatekeeper == nil {
		writeErr(w, http.StatusBadRequest, "the gatekeeper tools are not enabled")
		return
	}
	if req.Cluster && !req.Tools {
		writeErr(w, http.StatusBadRequest, "cluster tools need the gatekeeper tools")
		return
	}
	if req.Cluster && !s.d.Cluster.Read {
		writeErr(w, http.StatusConflict, "no cluster is configured")
		return
	}
	if req.IncidentID != nil && !req.Tools {
		writeErr(w, http.StatusBadRequest, "a question about an incident needs the gatekeeper tools")
		return
	}
	if req.IncidentID != nil && *req.IncidentID <= 0 {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	if len(req.Hosts) > 0 {
		switch {
		case !req.Tools:
			writeErr(w, http.StatusBadRequest, "hosts need the gatekeeper tools")
			return
		case len(req.Hosts) > maxRunHosts:
			writeErr(w, http.StatusBadRequest, "at most 8 hosts")
			return
		case s.d.Hosts == nil:
			writeErr(w, http.StatusConflict, "no hosts are configured")
			return
		}
		for _, id := range req.Hosts {
			e, err := s.d.Store.GetHost(r.Context(), id)
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusConflict, fmt.Sprintf("the host %d does not exist", id))
				return
			}
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "could not load the host")
				return
			}
			if why := notReady(e); why != "" {
				writeErr(w, http.StatusConflict, fmt.Sprintf("the host %s is not ready: %s", e.Host.Name, why))
				return
			}
		}
	}
	opts := store.RunOptions{Provider: req.Provider, Prompt: req.Prompt, Tools: req.Tools, Cluster: req.Cluster, HostIDs: req.Hosts}
	if req.IncidentID != nil {
		opts.IncidentID = *req.IncidentID
	}
	created, err := s.d.Store.CreateRunWith(r.Context(), opts)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	case errors.Is(err, store.ErrNoSuchHost):
		writeErr(w, http.StatusConflict, "a host does not exist")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not create run")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

const maxRunHosts = 8

// runHost is all a run's answer says about a host: no address, account, key or login.
type runHost struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// notReady says why a host cannot be given to a run, or "" when it can.
func notReady(e store.HostEntry) string {
	switch {
	case !e.Host.Enabled:
		return "it is disabled"
	case e.Credential == nil:
		return "it has no login"
	case e.Host.PinnedKey == "":
		return "its key is not pinned"
	}
	return ""
}

func (s *srv) listRuns(w http.ResponseWriter, r *http.Request) {
	if v := r.URL.Query().Get("incident"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, http.StatusBadRequest, "incident must be a positive whole number")
			return
		}
		runs, err := s.d.Store.ListIncidentRuns(r.Context(), id, 50)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not list runs")
			return
		}
		// A responder run's prompt holds the cleaned data from GitHub and its output the whole diagnosis: the thread needs neither
		// (the diagnosis is on the incident), so the list leaves them out. GET /api/runs/{id} still has everything.
		for i := range runs {
			if runs[i].Role == run.RoleResponder {
				runs[i].Prompt, runs[i].Result, runs[i].Output = "", "", nil
			}
		}
		writeJSON(w, http.StatusOK, runs)
		return
	}
	runs, err := s.d.Store.ListRuns(r.Context(), 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list runs")
		return
	}
	// A learning run's prompt holds the host's own text and its output the whole learning answer: the list leaves them out and names the
	// host instead. GET /api/runs/{id} still has everything.
	items := make([]runListItem, 0, len(runs))
	for _, rn := range runs {
		item := runListItem{Run: rn}
		if rn.Role == run.RoleLearner {
			item.Prompt, item.Result, item.Output = "", "", nil
			if entries, err := s.d.Store.RunHosts(r.Context(), rn.ID); err == nil {
				for _, e := range entries {
					item.Hosts = append(item.Hosts, runHost{ID: e.Host.ID, Name: e.Host.Name})
				}
			}
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *srv) getRun(w http.ResponseWriter, r *http.Request) {
	got, err := s.d.Store.GetRun(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load run")
		return
	}
	// A run that waits for an approval says which one, so that the UI can link to it.
	view := struct {
		run.Run
		WaitingApproval int64     `json:"waitingApproval,omitempty"`
		Hosts           []runHost `json:"hosts,omitempty"`
	}{Run: got}
	if got.MCP {
		view.WaitingApproval, _ = s.d.Store.PendingApprovalForRun(r.Context(), got.ID)
	}
	if got.HostCount > 0 {
		if entries, err := s.d.Store.RunHosts(r.Context(), got.ID); err == nil {
			for _, e := range entries {
				view.Hosts = append(view.Hosts, runHost{ID: e.Host.ID, Name: e.Host.Name})
			}
		}
	}
	writeJSON(w, http.StatusOK, view)
}
