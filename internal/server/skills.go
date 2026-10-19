package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

// maxSkillBody bounds a request: a body of 8 KB can grow when JSON escapes it.
const maxSkillBody = 64 << 10

type evidenceView struct {
	Argv []string `json:"argv"`
	Ran  bool     `json:"ran"`
}

// replacedView is the current text of the skill a proposal replaces, shown beside the proposal.
type replacedView struct {
	ID          int64  `json:"id"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

type skillView struct {
	ID          int64          `json:"id"`
	HostID      *int64         `json:"hostId"`
	Host        string         `json:"host,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Body        string         `json:"body"`
	Origin      string         `json:"origin"`
	State       string         `json:"state"`
	RunID       string         `json:"runId,omitempty"`
	Evidence    []evidenceView `json:"evidence"`
	Replaces    *replacedView  `json:"replaces,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	ConfirmedAt *time.Time     `json:"confirmedAt,omitempty"`
}

// skillViewOf builds the view of a skill; replaced is the skill a proposal replaces, if any. The body is text: the UI shows it as text.
func skillViewOf(k store.Skill, replaced *store.Skill) skillView {
	v := skillView{
		ID: k.ID, Host: k.HostName, Name: k.Name, Description: k.Description, Body: k.Body, Origin: k.Origin, State: k.State, RunID: k.RunID,
		Evidence: make([]evidenceView, 0, len(k.Evidence)), CreatedAt: k.CreatedAt, UpdatedAt: k.UpdatedAt, ConfirmedAt: k.ConfirmedAt,
	}
	if k.HostID != 0 {
		id := k.HostID
		v.HostID = &id
	}
	for _, e := range k.Evidence {
		argv := e.Argv
		if argv == nil {
			argv = []string{}
		}
		v.Evidence = append(v.Evidence, evidenceView{Argv: argv, Ran: e.Ran})
	}
	if replaced != nil {
		v.Replaces = &replacedView{ID: replaced.ID, Description: replaced.Description, Body: replaced.Body}
	}
	return v
}

func skillID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// skillError answers for an error of the store: a rule that was broken is a 400 with its text, a missing thing a 404 with the text
// the caller gives, a clash a 409. Every text is fixed: none carries what an agent wrote.
func (s *srv) skillError(w http.ResponseWriter, err error, missing, fallback string) {
	var fe *skill.FieldError
	switch {
	case errors.As(err, &fe):
		writeErr(w, http.StatusBadRequest, fe.Msg)
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, missing)
	case errors.Is(err, store.ErrScopeFull):
		writeErr(w, http.StatusConflict, fmt.Sprintf("This scope already has %d skills. Delete one first.", skill.MaxPerScope))
	case errors.Is(err, store.ErrExists):
		writeErr(w, http.StatusConflict, "A skill with this name already exists in this scope.")
	case errors.Is(err, store.ErrSkillState):
		writeErr(w, http.StatusConflict, "A proposal can only be confirmed or rejected, and only a proposal can be.")
	default:
		writeErr(w, http.StatusInternalServerError, fallback)
	}
}

func readSkillBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSkillBody)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// viewOfStored builds the view of a skill read from the store. A proposal is read together with the skill it replaces, in one
// transaction, so the review shows the text that a confirmation would overwrite. A proposal that was handled meanwhile is shown as
// it was read, without a replaced skill.
func (s *srv) viewOfStored(r *http.Request, k store.Skill) (skillView, error) {
	if k.State != store.SkillProposed {
		return skillViewOf(k, nil), nil
	}
	p, replaced, err := s.d.Store.ProposalWithReplaced(r.Context(), k.ID)
	switch {
	case err == nil:
		return skillViewOf(p, replaced), nil
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrSkillState):
		return skillViewOf(k, nil), nil
	default:
		return skillView{}, err
	}
}

func (s *srv) listSkills(w http.ResponseWriter, r *http.Request) {
	var f store.SkillFilter
	if v := r.URL.Query().Get("host"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, http.StatusBadRequest, "host must be the id of a host")
			return
		}
		f.HostID = id
	}
	switch state := r.URL.Query().Get("state"); state {
	case "", store.SkillProposed, store.SkillActive, store.SkillOff:
		f.State = state
	default:
		writeErr(w, http.StatusBadRequest, "state must be proposed, active or off")
		return
	}
	list, err := s.d.Store.ListSkills(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list the skills")
		return
	}
	views := make([]skillView, 0, len(list))
	for _, k := range list {
		v, err := s.viewOfStored(r, k)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not list the skills")
			return
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *srv) getSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := skillID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "skill not found")
		return
	}
	k, err := s.d.Store.GetSkill(r.Context(), id)
	if err != nil {
		s.skillError(w, err, "skill not found", "could not load the skill")
		return
	}
	v, err := s.viewOfStored(r, k)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the skill")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *srv) createSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HostID      *int64 `json:"hostId"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Body        string `json:"body"`
	}
	if !readSkillBody(w, r, &body) {
		return
	}
	var hostID int64
	if body.HostID != nil {
		if *body.HostID <= 0 {
			writeErr(w, http.StatusBadRequest, "hostId must be the id of a host, or left out for a global skill")
			return
		}
		hostID = *body.HostID
	}
	k, err := s.d.Store.CreateSkill(r.Context(), hostID, skill.Fields{Name: body.Name, Description: body.Description, Body: body.Body})
	if err != nil {
		s.skillError(w, err, "host not found", "could not save the skill")
		return
	}
	writeJSON(w, http.StatusCreated, skillViewOf(k, nil))
}

func (s *srv) patchSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := skillID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "skill not found")
		return
	}
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Body        *string `json:"body"`
		State       *string `json:"state"`
	}
	if !readSkillBody(w, r, &body) {
		return
	}
	cur, err := s.d.Store.GetSkill(r.Context(), id)
	if err != nil {
		s.skillError(w, err, "skill not found", "could not load the skill")
		return
	}
	if cur.State == store.SkillProposed { // the store refuses it too; this keeps the state "proposed" from being read as a bad state
		s.skillError(w, store.ErrSkillState, "", "")
		return
	}
	f := skill.Fields{Name: cur.Name, Description: cur.Description, Body: cur.Body}
	state := cur.State
	if body.Name != nil {
		f.Name = *body.Name
	}
	if body.Description != nil {
		f.Description = *body.Description
	}
	if body.Body != nil {
		f.Body = *body.Body
	}
	if body.State != nil {
		state = *body.State
	}
	k, err := s.d.Store.UpdateSkill(r.Context(), id, f, state)
	if err != nil {
		s.skillError(w, err, "skill not found", "could not save the skill")
		return
	}
	writeJSON(w, http.StatusOK, skillViewOf(k, nil))
}

func (s *srv) deleteSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := skillID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "skill not found")
		return
	}
	if err := s.d.Store.DeleteSkill(r.Context(), id); err != nil {
		s.skillError(w, err, "skill not found", "could not delete the skill")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) confirmSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := skillID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "skill not found")
		return
	}
	var body struct {
		Description *string `json:"description"`
		Body        *string `json:"body"`
	}
	// No body at all is a confirmation without an edit.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSkillBody)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cur, err := s.d.Store.GetSkill(r.Context(), id)
	if err != nil {
		s.skillError(w, err, "skill not found", "could not load the skill")
		return
	}
	var edit *skill.Fields
	if body.Description != nil || body.Body != nil {
		edit = &skill.Fields{Name: cur.Name, Description: cur.Description, Body: cur.Body}
		if body.Description != nil {
			edit.Description = *body.Description
		}
		if body.Body != nil {
			edit.Body = *body.Body
		}
	}
	k, err := s.d.Store.ConfirmSkill(r.Context(), id, edit)
	if err != nil {
		s.skillError(w, err, "skill not found", "could not confirm the skill")
		return
	}
	writeJSON(w, http.StatusOK, skillViewOf(k, nil))
}

func (s *srv) rejectSkill(w http.ResponseWriter, r *http.Request) {
	id, ok := skillID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "skill not found")
		return
	}
	if err := s.d.Store.RejectSkill(r.Context(), id); err != nil {
		s.skillError(w, err, "skill not found", "could not reject the proposal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
