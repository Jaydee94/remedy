package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Jaydee94/remedy/internal/store"
)

const maxPromptBytes = 20_000

func (s *srv) createRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Prompt   string `json:"prompt"`
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
	created, err := s.d.Store.CreateRun(r.Context(), req.Provider, req.Prompt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create run")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *srv) listRuns(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, got)
}
