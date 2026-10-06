package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

const maxPromptBytes = 20_000

func (s *srv) createRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider   string `json:"provider"`
		Prompt     string `json:"prompt"`
		Tools      bool   `json:"tools"`
		Cluster    bool   `json:"cluster"`
		IncidentID *int64 `json:"incidentId"`
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
	if req.IncidentID != nil {
		created, err := s.d.Store.CreateQuestionRun(r.Context(), req.Provider, req.Prompt, *req.IncidentID, req.Cluster)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "incident not found")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not create run")
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	create := s.d.Store.CreateRun
	switch {
	case req.Cluster:
		create = s.d.Store.CreateClusterRun
	case req.Tools:
		create = s.d.Store.CreateToolRun
	}
	created, err := create(r.Context(), req.Provider, req.Prompt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create run")
		return
	}
	writeJSON(w, http.StatusCreated, created)
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
	writeJSON(w, http.StatusOK, runs)
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
		WaitingApproval int64 `json:"waitingApproval,omitempty"`
	}{Run: got}
	if got.MCP {
		view.WaitingApproval, _ = s.d.Store.PendingApprovalForRun(r.Context(), got.ID)
	}
	writeJSON(w, http.StatusOK, view)
}
