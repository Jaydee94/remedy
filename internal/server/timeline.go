package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

const (
	defaultActivityLimit = 50
	maxActivityLimit     = 200
	activityBatch        = 100
	defaultActivityPoll  = time.Second
)

var errBadAfter = errors.New("after must be a whole number of 0 or more")

// timelineEntry is one line of the timeline. The optional fields are left out when they are empty.
type timelineEntry struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"`
	Summary    string    `json:"summary"`
	Repo       string    `json:"repo,omitempty"`
	IncidentID int64     `json:"incidentId,omitempty"`
	RunID      string    `json:"runId,omitempty"`
}

func timelineEntryOf(a store.Activity) timelineEntry {
	return timelineEntry{ID: a.ID, At: a.At, Kind: a.Kind, Summary: a.Summary, Repo: a.RepoName, IncidentID: a.IncidentID, RunID: a.RunID}
}

func (s *srv) listActivity(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := defaultActivityLimit
	if v := query.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxActivityLimit {
			writeErr(w, http.StatusBadRequest, "limit must be a whole number from 1 to "+strconv.Itoa(maxActivityLimit))
			return
		}
		limit = n
	}
	var before int64
	if v := query.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "before must be a positive whole number")
			return
		}
		before = n
	}

	// One row more than asked for tells whether there is another page.
	log, err := s.d.Store.ListActivity(r.Context(), store.ActivityQuery{Before: before, Limit: limit + 1})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the timeline")
		return
	}
	hasMore := len(log) > limit
	if hasMore {
		log = log[:limit]
	}
	entries := make([]timelineEntry, 0, len(log))
	for _, a := range log {
		entries = append(entries, timelineEntryOf(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "hasMore": hasMore})
}

// resumePoint is the id after which the stream starts. A browser that reconnects sends Last-Event-ID, which
// wins. A first connection names the newest entry it already has in ?after=. Without either, the stream
// starts at the end of the log.
func (s *srv) resumePoint(r *http.Request) (int64, error) {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n, nil
		}
	}
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, errBadAfter
		}
		return n, nil
	}
	return s.d.Store.LastActivityID(r.Context())
}

// streamActivity follows the activity log: every entry above the resume point, then new ones as they are
// written. The log is only ever appended to and ids grow, so "above the last id sent" misses nothing.
func (s *srv) streamActivity(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	last, err := s.resumePoint(r)
	switch {
	case errors.Is(err, errBadAfter):
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not read the activity")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	// The resume point is fixed before the headers go out, so whatever happens after a client sees them is sent.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	interval := s.d.ActivityInterval
	if interval <= 0 {
		interval = defaultActivityPoll
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastWrite := time.Now()

	for {
		entries, err := s.d.Store.ListActivitySince(r.Context(), last, activityBatch)
		if err != nil {
			return
		}
		for _, a := range entries {
			if writeSSE(w, strconv.FormatInt(a.ID, 10), "activity", timelineEntryOf(a)) != nil {
				return
			}
			last = a.ID
		}
		switch {
		case len(entries) > 0:
			flusher.Flush()
			lastWrite = time.Now()
		case time.Since(lastWrite) >= keepAlive:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
			lastWrite = time.Now()
		}
		if len(entries) == activityBatch {
			continue // a full batch: there may be more waiting
		}

		select {
		case <-ticker.C:
		case <-r.Context().Done():
			return
		}
	}
}
