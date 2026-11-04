// Package webhook is the inbound source of incidents: a monitoring tool posts one event to an endpoint with a bearer token, and
// the event opens, updates or resolves an incident of the source "webhook". Everything in a body is untrusted text; it is bounded and
// redacted here with the helpers of the Alertmanager source, and reaches an agent only through internal/prompt.
package webhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/redact"
	"github.com/Jaydee94/remedy/internal/signals"
)

// MaxBody is the largest body that is read.
const MaxBody = 64 << 10

const maxKey = 200

// ErrTooLarge is returned for a body over MaxBody.
var ErrTooLarge = errors.New("the body is too large")

// FieldError says which member of the body is wrong and why. It never repeats a submitted value.
type FieldError struct{ Field, Reason string }

func (e *FieldError) Error() string {
	if e.Field == "" {
		return e.Reason
	}
	return e.Field + ": " + e.Reason
}

// Event is one decoded, bounded event.
type Event struct {
	Key      string // the sender's key; the stored key is "<endpoint>/<Key>"
	Status   string // firing or resolved
	Title    string
	Severity string
	// Labels are all the labels of the body, for the host rules. Details holds the bounded copy that is stored.
	Labels   map[string]string
	Details  json.RawMessage
	URL      string
	StartsAt time.Time // zero when the body had none
}

// Firing says whether the event reports a problem (and not that it is over).
func (e Event) Firing() bool { return e.Status == "firing" }

type body struct {
	Version     *int               `json:"version"`
	Key         *string            `json:"key"`
	Status      *string            `json:"status"`
	Title       *string            `json:"title"`
	Severity    *string            `json:"severity"`
	Labels      map[string]*string `json:"labels"`
	Annotations map[string]*string `json:"annotations"`
	URL         *string            `json:"url"`
	StartsAt    *string            `json:"startsAt"`
}

func bad(field, reason string) error { return &FieldError{Field: field, Reason: reason} }

// Decode reads and checks one body (schema version 1). It reads at most MaxBody bytes and refuses unknown members, a wrong type, a
// second value after the object and every value outside its bounds.
func Decode(r io.Reader) (Event, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxBody+1))
	if err != nil {
		return Event{}, err
	}
	if len(raw) > MaxBody {
		return Event{}, ErrTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var b body
	if err := dec.Decode(&b); err != nil {
		return Event{}, decodeError(err)
	}
	// Nothing but white space may follow the object.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Event{}, bad("", "the body must be one JSON object")
	}

	if b.Version == nil || *b.Version != 1 {
		return Event{}, bad("version", "must be 1")
	}
	var e Event
	if e.Key, err = checkKey(b.Key); err != nil {
		return Event{}, err
	}
	if b.Status == nil || (*b.Status != "firing" && *b.Status != "resolved") {
		return Event{}, bad("status", "must be firing or resolved")
	}
	e.Status = *b.Status
	e.Severity = "none"
	if b.Severity != nil {
		switch *b.Severity {
		case "critical", "warning", "info", "none":
			e.Severity = *b.Severity
		default:
			return Event{}, bad("severity", "must be critical, warning, info or none")
		}
	}
	if b.Title != nil {
		t, _ := signals.Cut(redact.Redact(signals.OneLine(*b.Title)), signals.MaxTitle)
		e.Title = t
	}
	if e.Firing() && e.Title == "" {
		return Event{}, bad("title", "is required for a firing event")
	}
	if e.Labels, err = strings1(b.Labels, "labels"); err != nil {
		return Event{}, err
	}
	annotations, err := strings1(b.Annotations, "annotations")
	if err != nil {
		return Event{}, err
	}
	if b.URL != nil && *b.URL != "" {
		u, perr := url.Parse(*b.URL)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Event{}, bad("url", "must be an http or https address")
		}
		e.URL = signals.CleanURL(*b.URL)
	}
	if b.StartsAt != nil {
		t, perr := time.Parse(time.RFC3339, *b.StartsAt)
		if perr != nil {
			return Event{}, bad("startsAt", "must be a time in RFC 3339 format")
		}
		e.StartsAt = t.UTC()
	}

	labels, c1 := signals.Bounded(e.Labels, signals.MaxLabels, signals.MaxLabelValue)
	anns, c2 := signals.Bounded(annotations, signals.MaxAnnotations, signals.MaxAnnotation)
	d := map[string]any{"labels": labels, "annotations": anns, "generatorURL": e.URL}
	if !e.StartsAt.IsZero() {
		d["startsAt"] = e.StartsAt.Format(time.RFC3339)
	}
	if c1 || c2 {
		d["truncated"] = true
	}
	e.Details, _ = json.Marshal(d)
	return e, nil
}

// checkKey accepts 1 to 200 printable characters: no control, format, private-use or non-ASCII space characters, and nothing that
// redaction would change (a secret as a key would be stored in the clear).
func checkKey(k *string) (string, error) {
	if k == nil || *k == "" {
		return "", bad("key", "is required")
	}
	if !utf8.ValidString(*k) || utf8.RuneCountInString(*k) > maxKey {
		return "", bad("key", "must be 1 to 200 characters")
	}
	for _, r := range *k {
		if !unicode.IsPrint(r) {
			return "", bad("key", "must hold printable characters only")
		}
	}
	if redact.Redact(*k) != *k {
		return "", bad("key", "must not look like a secret")
	}
	return *k, nil
}

// strings1 turns an object of optional strings into a map; a null or a non-string value is refused.
func strings1(in map[string]*string, field string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if v == nil {
			return nil, bad(field, "every value must be a string")
		}
		out[k] = *v
	}
	return out, nil
}

// decodeError names the member of a type error, or the unknown member; it never repeats a value.
func decodeError(err error) error {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		field, _, _ := strings.Cut(te.Field, ".")
		return bad(field, "has the wrong type")
	}
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		field, _ := signals.Cut(signals.OneLine(strings.Trim(name, `"`)), 40) // the name is the sender's: bounded, not echoed whole
		return bad(field, "is not a member of version 1")
	}
	return bad("", "the body is not valid JSON")
}
