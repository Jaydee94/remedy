package signals

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/alertmanager"
	"github.com/Jaydee94/remedy/internal/redact"
)

// Limits of what an alert may put into an incident (the spec, section 5).
const (
	maxLabels      = 30
	maxLabelKey    = 64
	maxLabelValue  = 300
	maxAnnotations = 20
	maxAnnotation  = 1000
	maxTitle       = 200
	maxAlertName   = 200
	maxFingerprint = 64
	maxURL         = 2000
	defaultMaxAge  = time.Hour
)

// AlertLister is what the source needs from the client.
type AlertLister interface {
	ListAlerts(ctx context.Context) ([]alertmanager.Alert, error)
}

// Alertmanager is the source of the alerts of an Alertmanager.
type Alertmanager struct {
	// Client is nil-checked (a source without a client is off), but a typed nil pointer inside the interface is the caller's
	// responsibility: wire Client only when the pointer is non-nil.
	Client AlertLister
	// On says whether an address is set. MaxAge is how old (since startsAt) an alert may be to be diagnosed on its own (nil: one hour).
	On     func() bool
	MaxAge func() time.Duration
	Now    func() time.Time // default time.Now
}

func (a *Alertmanager) Name() string { return "alertmanager" }

func (a *Alertmanager) Enabled() bool { return a.Client != nil && a.On != nil && a.On() }

func (a *Alertmanager) maxAge() time.Duration {
	if a.MaxAge != nil {
		return a.MaxAge()
	}
	return defaultMaxAge
}

// cleanURL keeps a web address only: anything else (javascript:, data:, a bare string) becomes empty. What is kept is redacted and
// cut, in this order, so that a secret cannot survive at the cut.
func cleanURL(u string) string {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return ""
	}
	u, _ = cut(redact.Redact(u), maxURL)
	return u
}

func (a *Alertmanager) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Fetch lists the active alerts. Watchdog and InfoInhibitor are heartbeats, not outages; an alert without a name or a fingerprint cannot
// be keyed and is skipped.
func (a *Alertmanager) Fetch(ctx context.Context) ([]Signal, error) {
	if a.Client == nil {
		return nil, errors.New("the Alertmanager source has no client")
	}
	alerts, err := a.Client.ListAlerts(ctx)
	if err != nil {
		return nil, err
	}
	now, maxAge := a.now(), a.maxAge()
	seen := map[string]bool{}
	out := make([]Signal, 0, len(alerts))
	for _, al := range alerts {
		name := al.Labels["alertname"]
		if name == "" || al.Fingerprint == "" || name == "Watchdog" || name == "InfoInhibitor" {
			continue
		}
		// The key goes into the database as it is: an alert with an oversized name or fingerprint is not a signal.
		if utf8.RuneCountInString(name) > maxAlertName || utf8.RuneCountInString(al.Fingerprint) > maxFingerprint {
			continue
		}
		key := name + "/" + al.Fingerprint
		if seen[key] {
			continue
		}
		seen[key] = true
		sev := severityOf(al.Labels["severity"])
		al.GeneratorURL = cleanURL(al.GeneratorURL) // the cleaned address is the one that is stored, in the details and in the signal
		out = append(out, Signal{
			Key: key, Title: titleOf(al), Severity: sev,
			Auto:    (sev == "critical" || sev == "warning") && !al.StartsAt.IsZero() && now.Sub(al.StartsAt) <= maxAge,
			Details: detailsOf(al), Labels: al.Labels, URL: al.GeneratorURL, SeenAt: now,
		})
	}
	return out, nil
}

func severityOf(label string) string {
	switch s := strings.ToLower(strings.TrimSpace(label)); s {
	case "critical", "warning", "info":
		return s
	}
	return "none"
}

// oneLine turns every run of white space and control characters into one space and trims the ends.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
}

func cut(s string, max int) (string, bool) {
	if utf8.RuneCountInString(s) <= max {
		return s, false
	}
	return string([]rune(s)[:max]), true
}

func titleOf(al alertmanager.Alert) string {
	t := oneLine(al.Annotations["summary"])
	if t == "" {
		t = al.Labels["alertname"]
		if ns, pod := al.Labels["namespace"], al.Labels["pod"]; ns != "" && pod != "" {
			t += " in " + ns + "/" + pod
		}
		t = oneLine(t)
	}
	t, _ = cut(redact.Redact(t), maxTitle)
	return t
}

// bounded keeps at most max entries (the smallest keys first, so the cut is the same every cycle), drops keys that are too long or
// that redaction would change, and cuts and redacts the values. It says whether it left anything out.
func bounded(in map[string]string, max, maxValue int) (map[string]string, bool) {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]string{}
	cutAny := false
	for _, k := range keys {
		if utf8.RuneCountInString(k) > maxLabelKey || redact.Redact(k) != k {
			cutAny = true
			continue
		}
		if len(out) == max {
			cutAny = true
			continue
		}
		v, c := cut(redact.Redact(in[k]), maxValue)
		cutAny = cutAny || c
		out[k] = v
	}
	return out, cutAny
}

func detailsOf(al alertmanager.Alert) json.RawMessage {
	labels, c1 := bounded(al.Labels, maxLabels, maxLabelValue)
	annotations, c2 := bounded(al.Annotations, maxAnnotations, maxAnnotation)
	d := map[string]any{
		"labels": labels, "annotations": annotations, "fingerprint": al.Fingerprint,
		"generatorURL": al.GeneratorURL,
	}
	if !al.StartsAt.IsZero() {
		d["startsAt"] = al.StartsAt.UTC().Format(time.RFC3339)
	}
	if c1 || c2 {
		d["truncated"] = true
	}
	b, _ := json.Marshal(d)
	return b
}
