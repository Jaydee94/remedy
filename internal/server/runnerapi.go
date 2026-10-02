package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

const (
	claimWait     = 25 * time.Second
	claimInterval = 500 * time.Millisecond
)

// runner guards a runner-API handler with the shared bearer token.
func (s *srv) runner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.d.RunnerToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, "invalid runner token")
			return
		}
		next(w, r)
	}
}

// claim long-polls for the next queued run and returns 204 if none arrives in time.
func (s *srv) claim(w http.ResponseWriter, r *http.Request) {
	deadline := time.NewTimer(claimWait)
	defer deadline.Stop()
	tick := time.NewTicker(claimInterval)
	defer tick.Stop()
	for {
		claimed, err := s.d.Store.ClaimNext(r.Context())
		if err != nil {
			if r.Context().Err() == nil {
				writeErr(w, http.StatusInternalServerError, "claim failed")
			}
			return
		}
		if claimed != nil {
			writeJSON(w, http.StatusOK, claimed)
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *srv) postEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil || req.Kind == "" || len(req.Payload) == 0 {
		writeErr(w, http.StatusBadRequest, "kind and payload are required")
		return
	}
	if _, err := s.d.Store.GetRun(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load run")
		return
	}
	if _, err := s.d.Store.AppendEvent(r.Context(), id, req.Kind, req.Payload); err != nil {
		writeErr(w, http.StatusBadRequest, "could not store event")
		return
	}
	s.hub.notify(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) finish(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var out run.Outcome
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&out); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	err := s.d.Store.FinishRun(r.Context(), id, out)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "run not found or not running")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not finish run")
		return
	}
	s.hub.notify(id)
	w.WriteHeader(http.StatusNoContent)
}
