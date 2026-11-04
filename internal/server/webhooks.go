package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

const maxWebhookBody = 4 << 10

// webhookView is an endpoint as the page shows it. It has no token and no hash: the token is in the answer of a create or a
// rotate and nowhere else.
type webhookView struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	Enabled             bool       `json:"enabled"`
	AutoDiagnose        bool       `json:"autoDiagnose"`
	ResolveAfterSeconds int        `json:"resolveAfterSeconds"`
	RatePerMinute       int        `json:"ratePerMinute"`
	NewPerHour          int        `json:"newPerHour"`
	CreatedAt           time.Time  `json:"createdAt"`
	LastUsedAt          *time.Time `json:"lastUsedAt,omitempty"`
	PreviousValidUntil  *time.Time `json:"previousValidUntil,omitempty"`
	// The counters are in memory: a restart of the control plane forgets them.
	Accepted  int        `json:"accepted"`
	Rejected  int        `json:"rejected"`
	LastError string     `json:"lastError,omitempty"`
	LastAt    *time.Time `json:"lastAt,omitempty"`
}

func (s *srv) webhookViewOf(e store.WebhookEndpoint) webhookView {
	v := webhookView{
		ID: e.ID, Name: e.Name, Enabled: e.Enabled, AutoDiagnose: e.AutoDiagnose, ResolveAfterSeconds: int(e.ResolveAfter / time.Second),
		RatePerMinute: e.RatePerMinute, NewPerHour: e.NewPerHour, CreatedAt: e.CreatedAt, LastUsedAt: e.LastUsedAt, PreviousValidUntil: e.PreviousValidUntil,
	}
	if s.d.WebhookLimits != nil {
		st := s.d.WebhookLimits.Stats(e.ID)
		v.Accepted, v.Rejected, v.LastError = st.Accepted, st.Rejected, st.LastError
		if !st.LastAt.IsZero() {
			at := st.LastAt
			v.LastAt = &at
		}
	}
	return v
}

type webhookBody struct {
	Name                *string `json:"name"`
	Enabled             *bool   `json:"enabled"`
	AutoDiagnose        *bool   `json:"autoDiagnose"`
	ResolveAfterSeconds *int    `json:"resolveAfterSeconds"`
	RatePerMinute       *int    `json:"ratePerMinute"`
	NewPerHour          *int    `json:"newPerHour"`
}

func (b webhookBody) apply(f store.WebhookFields) store.WebhookFields {
	if b.Enabled != nil {
		f.Enabled = *b.Enabled
	}
	if b.AutoDiagnose != nil {
		f.AutoDiagnose = *b.AutoDiagnose
	}
	if b.ResolveAfterSeconds != nil {
		f.ResolveAfter = time.Duration(*b.ResolveAfterSeconds) * time.Second
	}
	if b.RatePerMinute != nil {
		f.RatePerMinute = *b.RatePerMinute
	}
	if b.NewPerHour != nil {
		f.NewPerHour = *b.NewPerHour
	}
	return f
}

// readWebhookBody decodes strictly: an unknown member (a token somebody tries to choose, for one) is an error.
func readWebhookBody(w http.ResponseWriter, r *http.Request) (webhookBody, bool) {
	var b webhookBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return b, false
	}
	return b, true
}

const badWebhook = "the name must be 1 to 40 characters: lower-case letters, digits and dashes, starting with a letter or a digit; " +
	"silence limit 0 or at least 300 seconds; 1 to 6000 requests a minute; 1 to 1000 new incidents an hour"

func webhookID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func (s *srv) listWebhooks(w http.ResponseWriter, r *http.Request) {
	list, err := s.d.Store.ListWebhooks(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list the webhook endpoints")
		return
	}
	views := make([]webhookView, 0, len(list))
	for _, e := range list {
		views = append(views, s.webhookViewOf(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"listening": s.d.WebhookListening, "endpoints": views})
}

// webhookCreated is the one answer that holds a token.
type webhookCreated struct {
	Endpoint webhookView `json:"endpoint"`
	Token    string      `json:"token"`
}

func noCache(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func (s *srv) createWebhook(w http.ResponseWriter, r *http.Request) {
	b, ok := readWebhookBody(w, r)
	if !ok {
		return
	}
	if b.Name == nil {
		writeErr(w, http.StatusBadRequest, badWebhook)
		return
	}
	defaults := store.WebhookFields{Enabled: true, ResolveAfter: 24 * time.Hour, RatePerMinute: 60, NewPerHour: 30}
	e, token, err := s.d.Store.CreateWebhook(r.Context(), *b.Name, b.apply(defaults))
	switch {
	case errors.Is(err, store.ErrBadWebhook):
		writeErr(w, http.StatusBadRequest, badWebhook)
		return
	case errors.Is(err, store.ErrExists):
		writeErr(w, http.StatusConflict, "an endpoint with this name already exists")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not create the endpoint")
		return
	}
	noCache(w)
	writeJSON(w, http.StatusCreated, webhookCreated{Endpoint: s.webhookViewOf(e), Token: token})
}

func (s *srv) patchWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := webhookID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "endpoint not found")
		return
	}
	b, ok := readWebhookBody(w, r)
	if !ok {
		return
	}
	if b.Name != nil {
		writeErr(w, http.StatusBadRequest, "the name of an endpoint does not change: its incidents are keyed by it")
		return
	}
	list, err := s.d.Store.ListWebhooks(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read the endpoint")
		return
	}
	var cur *store.WebhookEndpoint
	for i := range list {
		if list[i].ID == id {
			cur = &list[i]
		}
	}
	if cur == nil {
		writeErr(w, http.StatusNotFound, "endpoint not found")
		return
	}
	f := b.apply(store.WebhookFields{Enabled: cur.Enabled, AutoDiagnose: cur.AutoDiagnose, ResolveAfter: cur.ResolveAfter, RatePerMinute: cur.RatePerMinute, NewPerHour: cur.NewPerHour})
	e, err := s.d.Store.UpdateWebhook(r.Context(), id, f)
	switch {
	case errors.Is(err, store.ErrBadWebhook):
		writeErr(w, http.StatusBadRequest, badWebhook)
		return
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "endpoint not found")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not change the endpoint")
		return
	}
	writeJSON(w, http.StatusOK, s.webhookViewOf(e))
}

func (s *srv) rotateWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := webhookID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "endpoint not found")
		return
	}
	e, token, err := s.d.Store.RotateWebhookToken(r.Context(), id, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "endpoint not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not rotate the token")
		return
	}
	noCache(w)
	writeJSON(w, http.StatusOK, webhookCreated{Endpoint: s.webhookViewOf(e), Token: token})
}

func (s *srv) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := webhookID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "endpoint not found")
		return
	}
	err := s.d.Store.DeleteWebhook(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "endpoint not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not delete the endpoint")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
