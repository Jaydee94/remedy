package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

const keepAlive = 15 * time.Second

// hub wakes SSE streams when a run gets a new event or finishes.
type hub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

func newHub() *hub { return &hub{subs: map[string]map[chan struct{}]struct{}{}} }

func (h *hub) subscribe(runID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[runID] == nil {
		h.subs[runID] = map[chan struct{}]struct{}{}
	}
	h.subs[runID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs[runID], ch)
		if len(h.subs[runID]) == 0 {
			delete(h.subs, runID)
		}
	}
}

func (h *hub) notify(runID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[runID] {
		select {
		case ch <- struct{}{}:
		default: // a wake-up is already pending
		}
	}
}

func writeSSE(w http.ResponseWriter, id, event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if id != "" {
		fmt.Fprintf(w, "id: %s\n", id)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	return err
}

// streamEvents sends the backlog, then live events, then a final "done" event with the run.
// Reconnecting clients resume from Last-Event-ID.
func (s *srv) streamEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.d.Store.GetRun(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load run")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")

	last, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	wake, cancel := s.hub.subscribe(id)
	defer cancel()

	for {
		// Read the status before the events: the runner posts every event before it finishes
		// the run, so a terminal status guarantees the following read sees all events.
		current, err := s.d.Store.GetRun(r.Context(), id)
		if err != nil {
			return
		}
		events, err := s.d.Store.Events(r.Context(), id, last)
		if err != nil {
			return
		}
		for _, e := range events {
			if writeSSE(w, strconv.Itoa(e.Seq), "run_event", e) != nil {
				return
			}
			last = e.Seq
		}
		if current.Status.Terminal() {
			_ = writeSSE(w, "", "done", current)
			flusher.Flush()
			return
		}
		flusher.Flush()

		select {
		case <-wake:
		case <-time.After(keepAlive):
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
