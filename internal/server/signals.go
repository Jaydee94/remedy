package server

import (
	"net/http"
	"time"
)

type signalView struct {
	Name      string     `json:"name"`
	State     string     `json:"state"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Active    int        `json:"active"`
	Error     string     `json:"error,omitempty"`
}

// getSignals lists the signal sources with what the runner last learned about each. The list is never null.
func (s *srv) getSignals(w http.ResponseWriter, _ *http.Request) {
	states := s.d.Signals()
	views := make([]signalView, 0, len(states))
	for _, st := range states {
		v := signalView{Name: st.Name, State: st.State, Active: st.Active, Error: st.Error}
		if !st.CheckedAt.IsZero() {
			at := st.CheckedAt
			v.CheckedAt = &at
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": views})
}
