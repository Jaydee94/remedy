package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/snapshot"
	"github.com/Jaydee94/remedy/internal/store"
)

// claimFor is the answer to a runner that claimed a run. A responder run also gets the diagnosis schema and
// the order to download the repository snapshot first.
func claimFor(r run.Run, mcpToken string) run.Claim {
	c := run.Claim{Run: r, MCPToken: mcpToken}
	if r.Role == run.RoleResponder {
		c.Schema = json.RawMessage(diagnosis.Schema)
		c.Snapshot = true
	}
	return c
}

func (s *srv) diagnoseIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := incidentID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	started, err := s.d.Responder.Start(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "incident not found")
	case errors.Is(err, store.ErrNotDiagnosable):
		writeErr(w, http.StatusConflict, "this incident cannot be diagnosed: it is resolved, ignored or already being diagnosed")
	case errors.Is(err, responder.ErrSourceNotSupported):
		writeErr(w, http.StatusConflict, "the diagnosis of an incident from this source is not available yet")
	case errors.Is(err, store.ErrBusy):
		writeErr(w, http.StatusConflict, "another run is queued or running; try again when it has finished")
	case errors.Is(err, responder.ErrNoConnection):
		writeErr(w, http.StatusConflict, "GitHub is not connected, or the stored token cannot be used")
	case errors.Is(err, responder.ErrGitHub):
		writeErr(w, http.StatusBadGateway, "could not read the failing run from GitHub")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not start the diagnosis")
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"runId": started.ID})
	}
}

type limitsView struct {
	PollIntervalSeconds     int `json:"pollIntervalSeconds"`
	DiagnoseCooldownSeconds int `json:"diagnoseCooldownSeconds"`
	DiagnoseMaxPerIncident  int `json:"diagnoseMaxPerIncident"`
	DiagnoseMaxPerDay       int `json:"diagnoseMaxPerDay"`
	StaleRunMinutes         int `json:"staleRunMinutes"`
}

func (s *srv) getLimits(w http.ResponseWriter, _ *http.Request) {
	l := s.d.Responder.Limits
	writeJSON(w, http.StatusOK, limitsView{
		PollIntervalSeconds:     int(s.d.PollInterval.Seconds()),
		DiagnoseCooldownSeconds: int(l.Cooldown.Seconds()),
		DiagnoseMaxPerIncident:  l.MaxPerIncident,
		DiagnoseMaxPerDay:       l.MaxPerDay,
		StaleRunMinutes:         int(reaper.DefaultMaxAge.Minutes()),
	})
}

// snapshot streams the repository snapshot of a running responder run to the runner. The runner holds no
// GitHub credential: the archive is read here and filtered on its way (no secret files, size limits).
func (s *srv) snapshot(w http.ResponseWriter, r *http.Request) {
	body, err := s.d.Responder.OpenSnapshot(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "run not found")
		return
	case errors.Is(err, responder.ErrNotSnapshotRun):
		writeErr(w, http.StatusConflict, "this run takes no snapshot, or it is not running")
		return
	case errors.Is(err, responder.ErrNoConnection):
		writeErr(w, http.StatusConflict, "GitHub is not connected, or the stored token cannot be used")
		return
	case errors.Is(err, responder.ErrGitHub):
		writeErr(w, http.StatusBadGateway, "could not read the repository from GitHub")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not open the snapshot")
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "application/gzip")
	if err := snapshot.Filter(w, body, snapshot.Limits{}); err != nil {
		// The status is sent already; breaking the connection is the only way to tell the runner that the
		// archive is incomplete. net/http treats this panic as "abort quietly".
		panic(http.ErrAbortHandler)
	}
}
