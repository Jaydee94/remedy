package server

import (
	"errors"
	"net/http"

	"github.com/Jaydee94/remedy/internal/store"
)

// busyMessage is all a client is told about a delete that could not get its rows: the store's own sentence, fixed.
const busyMessage = "the database is busy; try again"

// writeBusy answers 503 with Retry-After: 2 when err carries store.ErrDatabaseBusy (a cascading delete whose lock waits kept
// running out; nothing was deleted and the same request may go through a moment later) and reports whether it did. The
// handlers of the four deletes that cascade call it before their other error answers.
func writeBusy(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, store.ErrDatabaseBusy) {
		return false
	}
	w.Header().Set("Retry-After", "2")
	writeErr(w, http.StatusServiceUnavailable, busyMessage)
	return true
}
