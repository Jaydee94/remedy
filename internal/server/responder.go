package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/prompt"
	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/snapshot"
	"github.com/Jaydee94/remedy/internal/store"
)

// claimFor is the answer to a runner that claimed a run. A responder run also gets the diagnosis schema and
// the order to download the repository snapshot first. A question about an incident (an ad-hoc run with an incident) gets the
// frame of prompt.Question around the stored question, and any ad-hoc run with gatekeeper access gets the list of the skills it may
// load; the stored prompt stays the bare question. A responder run carries its list in its stored prompt.
func claimFor(r run.Run, mcpToken string, skills []prompt.Skill) run.Claim {
	c := run.Claim{Run: r, MCPToken: mcpToken}
	if r.Role == run.RoleResponder {
		c.Schema = json.RawMessage(diagnosis.Schema)
		c.Snapshot = true
	}
	// A learning run carries its whole prompt (built when it was started, with the host and the skills it already has) and asks for the
	// learning answer; it has no repository to download.
	if r.Role == run.RoleLearner {
		c.Schema = json.RawMessage(diagnosis.LearnSchema)
	}
	switch {
	case r.Role == run.RoleAdhoc && r.IncidentID != nil:
		c.Prompt = prompt.QuestionWithSkills(*r.IncidentID, r.Prompt, skills)
	case r.Role == run.RoleAdhoc && r.MCP:
		c.Prompt = prompt.WithSkills(r.Prompt, skills)
	}
	return c
}

// claimSkills reads the skills an ad-hoc run with gatekeeper access may see, at the time of the claim.
func (s *srv) claimSkills(ctx context.Context, r run.Run) ([]prompt.Skill, error) {
	if r.Role != run.RoleAdhoc || !r.MCP {
		return nil, nil
	}
	list, err := s.d.Store.RunSkills(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	return responder.SkillLines(list), nil
}

// claimOf builds the answer to a claim. An error means that what the run needs could not be read (its incident or its skills): the
// caller ends the run instead of handing out a prompt that is wrong.
func (s *srv) claimOf(ctx context.Context, r run.Run, mcpToken string) (run.Claim, error) {
	skills, err := s.claimSkills(ctx, r)
	if err != nil {
		return run.Claim{}, err
	}
	c := claimFor(r, mcpToken, skills)
	if err := s.responderClaim(ctx, &c); err != nil {
		return run.Claim{}, err
	}
	return c, nil
}

// responderClaim sets what a responder run needs besides the run: the schema of the answer and whether to download a snapshot, by the
// source of its incident. A GitHub incident (and a run whose incident is gone) gets the diagnosis schema and the snapshot, as before.
// Only a missing incident means GitHub's shape; any other error of the lookup is returned, because guessing would hand an alert's run the
// wrong schema and a snapshot download that cannot work.
func (s *srv) responderClaim(ctx context.Context, c *run.Claim) error {
	if c.Role != run.RoleResponder || c.IncidentID == nil {
		return nil
	}
	in, err := s.d.Store.GetIncident(ctx, *c.IncidentID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil
	case err != nil:
		return err
	case in.Source == store.SourceAlertmanager:
		c.Schema = json.RawMessage(diagnosis.OutageSchema)
		c.Snapshot = false
	}
	return nil
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
	// DiagnosesLast24h is how many automatic diagnoses were started in the last 24 hours, counted the way the daily limit counts.
	DiagnosesLast24h int `json:"diagnosesLast24h"`
}

func (s *srv) getLimits(w http.ResponseWriter, r *http.Request) {
	l := s.d.Responder.Limits
	used, err := s.d.Store.AutoRunsSince(r.Context(), time.Now().Add(-24*time.Hour))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not count the diagnoses")
		return
	}
	view := limitsView{
		PollIntervalSeconds:     int(s.d.PollInterval.Seconds()),
		DiagnoseCooldownSeconds: int(l.Cooldown.Seconds()),
		DiagnoseMaxPerIncident:  l.MaxPerIncident,
		DiagnoseMaxPerDay:       l.MaxPerDay,
		StaleRunMinutes:         int(reaper.DefaultMaxAge.Minutes()),
		DiagnosesLast24h:        used,
	}
	if set := s.d.Settings; set != nil {
		limits := set.DiagnosisLimits()
		view.PollIntervalSeconds = int(set.Duration(settings.PollInterval).Seconds())
		view.DiagnoseCooldownSeconds = int(limits.Cooldown.Seconds())
		view.DiagnoseMaxPerIncident = limits.MaxPerIncident
		view.DiagnoseMaxPerDay = limits.MaxPerDay
		view.StaleRunMinutes = int(reaper.MaxAgeFor(set.Duration(settings.RunTimeout)).Minutes())
	}
	writeJSON(w, http.StatusOK, view)
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
