package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/store"
)

const maxReasonBytes = 500

// callView is a tool call as the admin API shows it: an audit row, and for a mutating tool an approval.
type callView struct {
	ID          int64           `json:"id"`
	RunID       string          `json:"runId"`
	IncidentID  int64           `json:"incidentId,omitempty"`
	Tool        string          `json:"tool"`
	Kind        string          `json:"kind"`
	Arguments   json.RawMessage `json:"arguments"`
	Status      string          `json:"status"`
	Decision    string          `json:"decision"`
	Reason      string          `json:"reason,omitempty"`
	Result      string          `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	RequestedAt time.Time       `json:"requestedAt"`
	DecidedAt   *time.Time      `json:"decidedAt,omitempty"`
	FinishedAt  *time.Time      `json:"finishedAt,omitempty"`
	// Waiting is true while an agent still waits for the answer of the call: only then can it be decided.
	Waiting bool `json:"waiting"`
}

func (s *srv) callViewOf(c store.ToolCall) callView {
	return callView{
		ID: c.ID, RunID: c.RunID, IncidentID: c.IncidentID, Tool: c.Tool, Kind: c.Kind, Arguments: c.Arguments,
		Status: c.Status, Decision: c.Decision, Reason: c.DecisionReason, Result: c.Result, Error: c.Error,
		RequestedAt: c.CreatedAt, DecidedAt: c.DecidedAt, FinishedAt: c.FinishedAt,
		Waiting: c.Decision == store.DecisionPending && s.d.Gatekeeper != nil && s.d.Gatekeeper.Waiting(c.ID),
	}
}

func (s *srv) viewsOf(calls []store.ToolCall) []callView {
	views := make([]callView, 0, len(calls))
	for _, c := range calls {
		views = append(views, s.callViewOf(c))
	}
	return views
}

func (s *srv) listApprovals(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	if status != "pending" && status != "all" {
		writeErr(w, http.StatusBadRequest, "status must be pending or all")
		return
	}
	list, err := s.d.Store.ListApprovals(r.Context(), store.ApprovalFilter{PendingOnly: status == "pending", Limit: 100})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list the approvals")
		return
	}
	writeJSON(w, http.StatusOK, s.viewsOf(list))
}

// decide handles the approve and the deny route.
func (s *srv) decide(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, http.StatusNotFound, "approval not found")
			return
		}
		var req struct {
			Reason string `json:"reason"`
		}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeErr(w, http.StatusBadRequest, "invalid request body")
				return
			}
		}
		if len(req.Reason) > maxReasonBytes {
			writeErr(w, http.StatusBadRequest, "the reason may have at most 500 bytes")
			return
		}

		c, err := s.d.Gatekeeper.Decide(r.Context(), id, approve, req.Reason)
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "approval not found")
		case errors.Is(err, gatekeeper.ErrNoWaiter):
			writeErr(w, http.StatusConflict, "the agent is no longer waiting for this call")
		case errors.Is(err, store.ErrNotPending):
			writeErr(w, http.StatusConflict, "this approval is not pending: it was decided, abandoned, or its run has ended")
		case err != nil:
			writeErr(w, http.StatusInternalServerError, "could not record the decision")
		default:
			writeJSON(w, http.StatusOK, s.callViewOf(c))
		}
	}
}

func (s *srv) listToolCalls(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.d.Store.GetRun(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the run")
		return
	}
	calls, err := s.d.Store.ListToolCalls(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list the tool calls")
		return
	}
	writeJSON(w, http.StatusOK, s.viewsOf(calls))
}

func (s *srv) cancelRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := s.d.Store.CancelRun(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "run not found")
	case errors.Is(err, store.ErrNotCancellable):
		writeErr(w, http.StatusConflict, "this run cannot be cancelled: it has ended, it belongs to a diagnosis, or it runs without gatekeeper access")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not cancel the run")
	default:
		s.hub.notify(id) // a queued run has just ended: let its event stream see it
		w.WriteHeader(http.StatusNoContent)
	}
}
