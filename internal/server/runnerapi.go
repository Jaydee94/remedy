package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/learner"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/settings"
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
		s.d.RunnerStatus.Touch(time.Now()) // an authenticated request means the runner is there
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
		claimed, err := s.claimGated(r.Context())
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
					done := context.WithoutCancel(r.Context())
					_ = s.d.Store.FinishRun(done, claimed.ID, run.Outcome{ExitCode: -1, Result: "The run token could not be created."})
					// A learning run that ends here tells its host, like every other failed learning run.
					if s.d.Learner != nil {
						s.d.Learner.Complete(done, claimed.ID)
					}
					writeErr(w, http.StatusInternalServerError, "could not create the run token")
					return
				}
			}
			c, err := s.claimOf(r.Context(), *claimed, token)
			if err != nil {
				// The run is claimed already: end it, like a run whose token could not be created, instead of leaving it running.
				done := context.WithoutCancel(r.Context())
				_ = s.d.Store.FinishRun(done, claimed.ID, run.Outcome{ExitCode: -1, Result: "The incident or the skills of the run could not be read."})
				if s.d.Responder != nil {
					s.d.Responder.Complete(done, claimed.ID)
				}
				if s.d.Learner != nil {
					s.d.Learner.Complete(done, claimed.ID)
				}
				writeErr(w, http.StatusInternalServerError, "could not read what the run needs")
				return
			}
			// Only a value somebody chose (the environment of the control plane or the Setup page) goes to the runner: while
			// the setting is at its default the field stays out, so a runner with its own REMEDY_RUN_TIMEOUT keeps it.
			if s.d.Settings != nil {
				if e, ok := s.d.Settings.Entry(settings.RunTimeout); ok && e.Source != settings.SourceDefault {
					c.RunTimeoutSeconds = int(e.Value.Dur / time.Second)
				}
			}
			writeJSON(w, http.StatusOK, c)
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

// claimGated is one ClaimNext behind the login relay's claim gate: while a login session is open nothing is claimed (nil,
// nil), and the gate is released only after ClaimNext has committed, so a login that starts next sees the claimed run.
func (s *srv) claimGated(ctx context.Context) (*run.Run, error) {
	if s.d.Login == nil {
		return s.d.Store.ClaimNext(ctx)
	}
	release, open := s.d.Login.ClaimGate()
	defer release()
	if !open {
		return nil, nil
	}
	return s.d.Store.ClaimNext(ctx)
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
			out = s.d.Responder.CheckRun(r.Context(), rn, out)
		}
	}
	// The same for a learning run: its answer must be valid, or the run counts as failed and nothing is stored.
	learnerRun := false
	if s.d.Learner != nil {
		if rn, err := s.d.Store.GetRun(r.Context(), id); err == nil && rn.Role == run.RoleLearner {
			learnerRun = true
			out = learner.CheckRun(out)
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
	// The run has ended: what follows must not depend on the runner's connection. With the request's context a connection that drops
	// now would abort the diagnosis or the proposals silently, and a repeated finish finds the run ended and completes nothing.
	done := context.WithoutCancel(r.Context())
	if responderRun {
		s.d.Responder.Complete(done, id)
	}
	if learnerRun {
		s.d.Learner.Complete(done, id)
	}
	s.hub.notify(id)
	w.WriteHeader(http.StatusNoContent)
}
