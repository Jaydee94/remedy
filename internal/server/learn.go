package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jaydee94/remedy/internal/learner"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

// learnHost starts the learning run of a host: 202 with the id of the run, or a 409 with a fixed sentence. Nothing an agent or a host
// wrote is in any of them.
func (s *srv) learnHost(w http.ResponseWriter, r *http.Request) {
	id, ok := hostID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	started, err := s.d.Learner.Start(r.Context(), id)
	var notReady *learner.NotReadyError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "host not found")
	case errors.As(err, &notReady):
		writeErr(w, http.StatusConflict, notReady.Error())
	case errors.Is(err, store.ErrLearning):
		writeErr(w, http.StatusConflict, "a learning run for this host is already queued or running")
	case errors.Is(err, store.ErrBusy):
		writeErr(w, http.StatusConflict, "another run is queued or running; try again when it has finished")
	case errors.Is(err, store.ErrProposalsWaiting):
		writeErr(w, http.StatusConflict, "too many proposals of this host wait for your review; confirm or reject some on the Skills page first")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not start the learning run")
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"runId": started.ID})
	}
}

// learningView is what the host's page needs to know about its newest learning run.
type learningView struct {
	RunID         string     `json:"runId"`
	Status        string     `json:"status"`
	FailureReason string     `json:"failureReason,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
}

// learningOf reads the newest learning run of a host; nil when there is none.
func (s *srv) learningOf(r *http.Request, hostID int64) *learningView {
	last, ok, err := s.d.Store.LastLearnerRun(r.Context(), hostID)
	if err != nil || !ok {
		return nil
	}
	return &learningView{RunID: last.ID, Status: string(last.Status), FailureReason: last.FailureReason, CreatedAt: last.CreatedAt, FinishedAt: last.FinishedAt}
}

// runListItem is a run in the list. A learning run also names the host it is about (the list leaves out the prompt of such a run, which
// holds the host's own text, and its answer).
type runListItem struct {
	run.Run
	Hosts []runHost `json:"hosts,omitempty"`
}
