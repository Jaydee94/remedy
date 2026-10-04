package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

// heartbeat is what the runner of a run with gatekeeper access calls every few seconds. The answer tells it whether
// the run waits for an approval (its time limit stands still) and whether it was cancelled (it stops the agent). A 404
// means the control plane no longer has the run as running, which the runner treats like a cancel.
func (s *srv) heartbeat(w http.ResponseWriter, r *http.Request) {
	beat, err := s.d.Store.Heartbeat(r.Context(), r.PathValue("id"), time.Now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "run not found or not running")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not record the heartbeat")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"waiting": beat.Waiting, "cancel": beat.Cancel})
	}
}
