package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/responder"
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
			// A run with gatekeeper access gets its token now: only a hash of it is kept, so the claim is the one
			// moment it can be handed out. A run that cannot get one fails instead of running without tools.
			token := ""
			if claimed.MCP {
				if token, err = s.d.Store.MintRunToken(r.Context(), claimed.ID); err != nil {
					_ = s.d.Store.FinishRun(context.WithoutCancel(r.Context()), claimed.ID,
						run.Outcome{ExitCode: -1, Result: "The run token could not be created."})
					writeErr(w, http.StatusInternalServerError, "could not create the run token")
					return
				}
			}
			writeJSON(w, http.StatusOK, claimFor(*claimed, token))
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
	if out.FailureReason != "" && out.FailureReason != run.ReasonTimeout && out.FailureReason != run.ReasonCancelled {
		writeErr(w, http.StatusBadRequest, "unknown failure reason")
		return
	}
	// What an agent answers is never trusted: a responder run must carry a valid diagnosis, or it counts
	// as failed.
	responderRun := false
	if s.d.Responder != nil {
		if rn, err := s.d.Store.GetRun(r.Context(), id); err == nil && rn.Role == run.RoleResponder {
			responderRun = true
			out = responder.CheckOutcome(out)
		}
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
	if responderRun {
		s.d.Responder.Complete(r.Context(), id)
	}
	s.hub.notify(id)
	w.WriteHeader(http.StatusNoContent)
}
