package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

type incidentView struct {
	ID             int64      `json:"id"`
	RepoID         int64      `json:"repoId"`
	Repo           string     `json:"repo"`
	Ref            string     `json:"ref"`
	RefURL         string     `json:"refUrl,omitempty"`
	CheckName      string     `json:"checkName"`
	State          string     `json:"state"`
	Conclusion     string     `json:"conclusion"`
	HeadSHA        string     `json:"headSha"`
	CheckURL       string     `json:"checkUrl,omitempty"`
	Occurrences    int        `json:"occurrences"`
	FirstSeen      time.Time  `json:"firstSeen"`
	LastSeen       time.Time  `json:"lastSeen"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
	ResolvedReason string     `json:"resolvedReason,omitempty"`

	Diagnoses       int             `json:"diagnoses"`
	LastDiagnosisAt *time.Time      `json:"lastDiagnosisAt,omitempty"`
	Diagnosis       json.RawMessage `json:"diagnosis,omitempty"`
	DiagnosedSHA    string          `json:"diagnosedSha,omitempty"`
	RunID           string          `json:"runId,omitempty"`
}

func incidentViewOf(in store.Incident) incidentView {
	return incidentView{
		ID: in.ID, RepoID: in.RepoID, Repo: in.RepoName, Ref: in.Ref, RefURL: in.RefURL, CheckName: in.CheckName,
		State: string(in.State), Conclusion: in.Conclusion, HeadSHA: in.HeadSHA, CheckURL: in.CheckURL,
		Occurrences: in.Occurrences, FirstSeen: in.FirstSeen, LastSeen: in.LastSeen,
		ResolvedAt: in.ResolvedAt, ResolvedReason: in.ResolvedReason,
		Diagnoses: in.Diagnoses, LastDiagnosisAt: in.LastDiagnosisAt, Diagnosis: in.Diagnosis,
		DiagnosedSHA: in.DiagnosedSHA, RunID: in.RunID,
	}
}

type activityView struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Summary string    `json:"summary"`
}

func validStateFilter(s string) bool {
	switch s {
	case "", "all", "active", "open", "diagnosing", "diagnosed", "resolved", "ignored":
		return true
	}
	return false
}

// incidentID parses the {id} path value. ok is false for anything that is not a number.
func incidentID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func (s *srv) listIncidents(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if !validStateFilter(state) {
		writeErr(w, http.StatusBadRequest, "unknown state")
		return
	}
	var repoID int64
	if v := r.URL.Query().Get("repo"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, http.StatusBadRequest, "repo must be a repository id")
			return
		}
		repoID = id
	}

	list, err := s.d.Store.ListIncidents(r.Context(), store.IncidentFilter{State: state, RepoID: repoID, Limit: 200})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list incidents")
		return
	}
	views := make([]incidentView, 0, len(list))
	for _, in := range list {
		views = append(views, incidentViewOf(in))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *srv) getIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := incidentID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	in, err := s.d.Store.GetIncident(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the incident")
		return
	}
	log, err := s.d.Store.ListActivity(r.Context(), store.ActivityQuery{IncidentID: id, Limit: 100})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the history")
		return
	}
	history := make([]activityView, 0, len(log))
	for _, a := range log {
		history = append(history, activityView{ID: a.ID, At: a.At, Kind: a.Kind, Summary: a.Summary})
	}
	writeJSON(w, http.StatusOK, map[string]any{"incident": incidentViewOf(in), "activity": history})
}

func (s *srv) ignoreIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := incidentID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	in, err := s.d.Incidents.Ignore(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "incident not found")
	case errors.Is(err, incident.ErrNotActive):
		writeErr(w, http.StatusConflict, "this incident is already resolved or ignored")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not ignore the incident")
	default:
		writeJSON(w, http.StatusOK, incidentViewOf(in))
	}
}
