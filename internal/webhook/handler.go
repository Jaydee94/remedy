package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/signals"
	"github.com/Jaydee94/remedy/internal/store"
)

// hostCacheFor is how long the host rules are kept: a webhook may arrive many times a second, the poller reads them once a cycle.
const hostCacheFor = 5 * time.Second

// Handler serves POST /webhook/v1/{endpoint}. The route is registered by server.NewWebhook, on the third listener only.
type Handler struct {
	Store interface {
		FindActiveIncidentByKey(ctx context.Context, source, key string) (store.Incident, error)
		TouchWebhook(ctx context.Context, id int64, at time.Time) error
	}
	Engine *incident.Engine
	Auth   *Authenticator
	Limits *Limits
	// Hosts reads the alert rules of the hosts; AutoMaxAge is how old (since startsAt) an event may be to be diagnosed on its own.
	Hosts      func(ctx context.Context) ([]signals.HostRules, error)
	AutoMaxAge func() time.Duration
	Now        func() time.Time // default time.Now
	Log        *slog.Logger

	mu       sync.Mutex
	rules    signals.CompiledHosts
	rulesAt  time.Time
	rulesSet bool
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	reply(w, code, map[string]string{"error": msg})
}

func retryAfter(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int((d+time.Second-1)/time.Second)))
}

// hostRules returns the compiled rules, read at most every hostCacheFor.
func (h *Handler) hostRules(ctx context.Context) (signals.CompiledHosts, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rulesSet && h.now().Sub(h.rulesAt) < hostCacheFor {
		return h.rules, nil
	}
	hosts, err := h.Hosts(ctx)
	if err != nil {
		return nil, err
	}
	rules, skipped := signals.CompileHosts(hosts)
	if len(skipped) > 0 {
		h.log().Warn("the alert rules of these hosts cannot be read and are skipped", "hosts", skipped)
	}
	h.rules, h.rulesAt, h.rulesSet = rules, h.now(), true
	return rules, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A full listener answers before it touches anything.
	release, ok := h.Limits.Acquire()
	if !ok {
		retryAfter(w, time.Second)
		fail(w, http.StatusServiceUnavailable, "busy")
		return
	}
	defer release()

	ep, ok, err := h.Auth.Check(r.Context(), r.PathValue("endpoint"), r.Header.Get("Authorization"))
	if err != nil {
		h.log().Error("webhook: the endpoint could not be read", "err", err)
		fail(w, http.StatusInternalServerError, "could not record the event")
		return
	}
	if !ok {
		Unauthorized(w)
		return
	}
	now := h.now()

	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		h.Limits.Record(ep.ID, false, "content type must be application/json", now)
		fail(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	if ok, wait := h.Limits.Allow(ep.ID, ep.RatePerMinute, now); !ok {
		h.Limits.Record(ep.ID, false, "rate limit", now)
		retryAfter(w, wait)
		fail(w, http.StatusTooManyRequests, "too many requests")
		return
	}

	ev, err := Decode(r.Body)
	var fe *FieldError
	switch {
	case errors.Is(err, ErrTooLarge):
		h.Limits.Record(ep.ID, false, "body too large", now)
		fail(w, http.StatusRequestEntityTooLarge, "the body is larger than 64 KiB")
		return
	case errors.As(err, &fe):
		h.Limits.Record(ep.ID, false, fe.Error(), now)
		reply(w, http.StatusBadRequest, map[string]string{"error": fe.Reason, "field": fe.Field})
		return
	case err != nil:
		h.Limits.Record(ep.ID, false, "the body could not be read", now)
		fail(w, http.StatusBadRequest, "the body could not be read")
		return
	}

	result, status, msg := h.apply(r.Context(), ep, ev, now)
	if status != http.StatusOK {
		h.Limits.Record(ep.ID, false, msg, now)
		if status == http.StatusTooManyRequests {
			retryAfter(w, time.Minute)
		}
		fail(w, status, msg)
		return
	}
	if err := h.Store.TouchWebhook(r.Context(), ep.ID, now); err != nil {
		h.log().Warn("webhook: last_used_at could not be written", "endpoint", ep.Name, "err", err)
	}
	h.Limits.Record(ep.ID, true, "", now)
	reply(w, http.StatusOK, map[string]string{"result": result})
}

// apply gives one decoded event to the engine and says what came of it: the result word, or an HTTP status and a message.
func (h *Handler) apply(ctx context.Context, ep store.WebhookEndpoint, ev Event, now time.Time) (result string, status int, msg string) {
	key := ep.Name + "/" + ev.Key
	cur, err := h.Store.FindActiveIncidentByKey(ctx, store.SourceWebhook, key)
	known := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		h.log().Error("webhook: the incident could not be read", "endpoint", ep.Name, "err", err)
		return "", http.StatusInternalServerError, "could not record the event"
	}

	if !ev.Firing() {
		if !known {
			return "ignored", http.StatusOK, "" // nothing to resolve: a retry or a resolve for a key never seen
		}
		_, err := h.Engine.ObserveSignal(ctx, incident.Observation{Source: store.SourceWebhook, Key: key, Class: incident.Green, Conclusion: "resolved"})
		if err != nil {
			h.log().Error("webhook: the incident could not be resolved", "endpoint", ep.Name, "err", err)
			return "", http.StatusInternalServerError, "could not record the event"
		}
		return "resolved", http.StatusOK, ""
	}

	if !known && !h.Limits.AllowNew(ep.ID, ep.NewPerHour, now) {
		return "", http.StatusTooManyRequests, "too many new incidents for this endpoint in the last hour"
	}
	rules, err := h.hostRules(ctx)
	if err != nil {
		h.log().Error("webhook: the hosts could not be read", "endpoint", ep.Name, "err", err)
		return "", http.StatusInternalServerError, "could not record the event"
	}
	host, also := signals.MapHost(rules, ev.Labels)
	wasNew, err := h.Engine.ObserveSignal(ctx, incident.Observation{
		Source: store.SourceWebhook, Key: key, Title: ev.Title, Severity: ev.Severity, AutoDiagnose: h.auto(ep, ev, now),
		Details: signals.WithAlso(ev.Details, also), HostID: host, Class: incident.Bad, Conclusion: "firing", URL: ev.URL,
	})
	if err != nil {
		h.log().Error("webhook: the incident could not be recorded", "endpoint", ep.Name, "err", err)
		return "", http.StatusInternalServerError, "could not record the event"
	}
	switch {
	case wasNew:
		return "opened", http.StatusOK, ""
	case known && cur.State == store.IncIgnored:
		return "ignored", http.StatusOK, ""
	}
	return "updated", http.StatusOK, ""
}

// auto decides, once, when the incident opens, whether the responder may start on its own: the endpoint allows it, the severity
// is critical or warning and the event is young. The same rule as an alert's.
func (h *Handler) auto(ep store.WebhookEndpoint, ev Event, now time.Time) bool {
	if !ep.AutoDiagnose || (ev.Severity != "critical" && ev.Severity != "warning") {
		return false
	}
	since := ev.StartsAt
	if since.IsZero() {
		since = now
	}
	maxAge := time.Hour
	if h.AutoMaxAge != nil {
		maxAge = h.AutoMaxAge()
	}
	return now.Sub(since) <= maxAge
}
