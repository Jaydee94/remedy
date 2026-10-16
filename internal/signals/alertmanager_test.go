package signals_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/alertmanager"
	"github.com/Jaydee94/remedy/internal/signals"
)

type lister struct {
	alerts []alertmanager.Alert
	err    error
}

func (l *lister) ListAlerts(context.Context) ([]alertmanager.Alert, error) { return l.alerts, l.err }

func alert(fp, name string, labels map[string]string, startsAt time.Time) alertmanager.Alert {
	a := alertmanager.Alert{Fingerprint: fp, Labels: map[string]string{"alertname": name}, Annotations: map[string]string{}, StartsAt: startsAt}
	for k, v := range labels {
		a.Labels[k] = v
	}
	a.Status.State = "active"
	return a
}

var now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func source(l *lister, maxAge time.Duration) *signals.Alertmanager {
	return &signals.Alertmanager{Client: l, On: func() bool { return true }, MaxAge: func() time.Duration { return maxAge }, Now: func() time.Time { return now }}
}

func fetch(t *testing.T, l *lister, maxAge time.Duration) []signals.Signal {
	t.Helper()
	got, err := source(l, maxAge).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTheSourceNamesItselfAndFollowsItsSwitch(t *testing.T) {
	on := false
	s := &signals.Alertmanager{Client: &lister{}, On: func() bool { return on }}
	if s.Name() != "alertmanager" || s.Enabled() {
		t.Fatal("the source is on while its switch is off")
	}
	on = true
	if !s.Enabled() {
		t.Fatal("the source is off while its switch is on")
	}
}

func TestAnAlertBecomesASignalWithItsKeyAndSeverity(t *testing.T) {
	l := &lister{alerts: []alertmanager.Alert{
		alert("f1", "KubePodCrashLooping", map[string]string{"severity": "CRITICAL", "namespace": "demo", "pod": "web-1"}, now.Add(-time.Minute)),
		alert("f2", "Odd", map[string]string{"severity": "page"}, now.Add(-time.Minute)),
		alert("f3", "NoSeverity", nil, now.Add(-time.Minute)),
	}}
	got := fetch(t, l, time.Hour)
	if len(got) != 3 || got[0].Key != "KubePodCrashLooping/f1" || got[0].Severity != "critical" || got[1].Severity != "none" || got[2].Severity != "none" {
		t.Fatalf("%+v", got)
	}
	if got[0].Labels["pod"] != "web-1" || got[0].SeenAt != now {
		t.Errorf("labels = %v, seen = %v", got[0].Labels, got[0].SeenAt)
	}
}

func TestWatchdogAndInfoInhibitorAndIncompleteAlertsAreNotSignals(t *testing.T) {
	l := &lister{alerts: []alertmanager.Alert{
		alert("w", "Watchdog", nil, now), alert("i", "InfoInhibitor", nil, now),
		alert("", "NoFingerprint", nil, now), alert("x", "", nil, now), alert("ok", "Real", nil, now),
		alert("ok", "Real", nil, now), // the same key twice
	}}
	got := fetch(t, l, time.Hour)
	if len(got) != 1 || got[0].Key != "Real/ok" {
		t.Fatalf("%+v", got)
	}
}

func TestOnlyAYoungCriticalOrWarningAlertIsDiagnosedOnItsOwn(t *testing.T) {
	young, old := now.Add(-10*time.Minute), now.Add(-3*time.Hour)
	l := &lister{alerts: []alertmanager.Alert{
		alert("a", "A", map[string]string{"severity": "critical"}, young),
		alert("b", "B", map[string]string{"severity": "warning"}, young),
		alert("c", "C", map[string]string{"severity": "info"}, young),
		alert("d", "D", nil, young),
		alert("e", "E", map[string]string{"severity": "critical"}, old),
		alert("f", "F", map[string]string{"severity": "critical"}, time.Time{}),
		alert("g", "G", map[string]string{"severity": "warning"}, now.Add(-time.Hour)), // exactly the maximum age: still young
		alert("h", "H", map[string]string{"severity": "warning"}, now.Add(time.Hour)),  // starts in the future: young
	}}
	want := map[string]bool{"A/a": true, "B/b": true, "C/c": false, "D/d": false, "E/e": false, "F/f": false, "G/g": true, "H/h": true}
	for _, s := range fetch(t, l, time.Hour) {
		if s.Auto != want[s.Key] {
			t.Errorf("%s: Auto = %v, want %v", s.Key, s.Auto, want[s.Key])
		}
	}
}

func TestTheTitleIsTheSummaryOrTheNameWithItsPlace(t *testing.T) {
	a1 := alert("1", "A", map[string]string{"namespace": "demo", "pod": "web-1"}, now)
	a1.Annotations["summary"] = "  The pod\nweb-1 keeps\trestarting  "
	a2 := alert("2", "B", map[string]string{"namespace": "demo", "pod": "web-2"}, now)
	a3 := alert("3", "C", nil, now)
	a4 := alert("4", "D", nil, now)
	a4.Annotations["summary"] = strings.Repeat("x", 500)
	got := fetch(t, &lister{alerts: []alertmanager.Alert{a1, a2, a3, a4}}, time.Hour)
	if got[0].Title != "The pod web-1 keeps restarting" || got[1].Title != "B in demo/web-2" || got[2].Title != "C" {
		t.Fatalf("titles = %q, %q, %q", got[0].Title, got[1].Title, got[2].Title)
	}
	if n := len([]rune(got[3].Title)); n != 200 {
		t.Errorf("a long title has %d characters, want 200", n)
	}
	for _, s := range got {
		if strings.ContainsAny(s.Title, "\n\r\t") {
			t.Errorf("the title %q has a control character", s.Title)
		}
	}
}

func TestTheDetailsAreBoundedAndRedacted(t *testing.T) {
	labels := map[string]string{"severity": "warning"}
	for i := 0; i < 40; i++ {
		labels["l"+string(rune('a'+i%26))+string(rune('a'+i/26))] = "v"
	}
	labels[strings.Repeat("k", 65)] = "key too long"
	labels["long"] = strings.Repeat("v", 400)
	a := alert("1", "A", labels, now)
	for i := 0; i < 25; i++ {
		a.Annotations["n"+string(rune('a'+i))] = "x"
	}
	a.Annotations["description"] = strings.Repeat("d", 1500)
	// internal/redact turns these into password=[REDACTED] and [REDACTED:aws-key] (see redact_test.go).
	a.Annotations["leak"] = "password=hunter2hunter2 and AKIAIOSFODNN7EXAMPLE"
	a.Annotations["summary"] = "login failed, password=hunter2hunter2 AKIAIOSFODNN7EXAMPLE"
	got := fetch(t, &lister{alerts: []alertmanager.Alert{a}}, time.Hour)[0]
	var d struct {
		Labels       map[string]string `json:"labels"`
		Annotations  map[string]string `json:"annotations"`
		StartsAt     string            `json:"startsAt"`
		Fingerprint  string            `json:"fingerprint"`
		GeneratorURL string            `json:"generatorURL"`
		Truncated    bool              `json:"truncated"`
	}
	if err := json.Unmarshal(got.Details, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Labels) > 30 || len(d.Annotations) > 20 || !d.Truncated || d.Fingerprint != "1" || d.StartsAt == "" {
		t.Fatalf("labels %d, annotations %d, truncated %v, %+v", len(d.Labels), len(d.Annotations), d.Truncated, d)
	}
	for k, v := range d.Labels {
		if len(k) > 64 || len([]rune(v)) > 300 {
			t.Errorf("label %q is not bounded", k)
		}
	}
	for k, v := range d.Annotations {
		if len([]rune(v)) > 1000 {
			t.Errorf("annotation %q is not bounded", k)
		}
	}
	for _, secret := range []string{"hunter2hunter2", "AKIAIOSFODNN7EXAMPLE"} {
		if strings.Contains(string(got.Details), secret) {
			t.Errorf("%s survived in the details: %s", secret, got.Details)
		}
		if strings.Contains(got.Title, secret) {
			t.Errorf("%s survived in the title: %s", secret, got.Title)
		}
	}
	if len(got.Labels) != len(a.Labels) { // a.Labels is labels plus the alertname that alert() adds
		t.Errorf("the labels used for the host mapping were cut: %d of %d", len(got.Labels), len(a.Labels))
	}
}

func detailsURL(t *testing.T, s signals.Signal) string {
	t.Helper()
	var d struct {
		GeneratorURL string `json:"generatorURL"`
	}
	if err := json.Unmarshal(s.Details, &d); err != nil {
		t.Fatal(err)
	}
	return d.GeneratorURL
}

func TestTheGeneratorURLIsKeptOnlyAsABoundedRedactedWebAddress(t *testing.T) {
	mk := func(fp, url string) alertmanager.Alert {
		a := alert(fp, "A"+fp, nil, now)
		a.GeneratorURL = url
		return a
	}
	long := "https://prom.example/graph?g0.expr=" + strings.Repeat("x", 5000)
	got := fetch(t, &lister{alerts: []alertmanager.Alert{
		mk("1", "javascript:alert(1)"),
		mk("2", long),
		mk("3", "https://prom.example/?password=hunter2hunter2"),
		mk("4", "http://prom.example/graph"),
		mk("5", ""),
	}}, time.Hour)
	if len(got) != 5 {
		t.Fatalf("%d signals", len(got))
	}
	for _, s := range got {
		if s.URL != detailsURL(t, s) {
			t.Errorf("%s: URL %q and details URL %q differ", s.Key, s.URL, detailsURL(t, s))
		}
	}
	if got[0].URL != "" {
		t.Errorf("a javascript: address was kept: %q", got[0].URL)
	}
	if n := len([]rune(got[1].URL)); n != 2000 || !strings.HasPrefix(got[1].URL, "https://prom.example/") {
		t.Errorf("a long address has %d characters, want 2000", n)
	}
	if strings.Contains(got[2].URL, "hunter2hunter2") || !strings.HasPrefix(got[2].URL, "https://prom.example/") {
		t.Errorf("the address is not redacted: %q", got[2].URL)
	}
	if got[3].URL != "http://prom.example/graph" || got[4].URL != "" {
		t.Errorf("urls = %q, %q", got[3].URL, got[4].URL)
	}
}

func TestAnAlertWithAHugeNameOrFingerprintIsNotASignal(t *testing.T) {
	got := fetch(t, &lister{alerts: []alertmanager.Alert{
		alert("1", strings.Repeat("n", 201), nil, now),
		alert(strings.Repeat("f", 65), "Long", nil, now),
		alert(strings.Repeat("f", 64), "Edge", nil, now),
		alert("2", strings.Repeat("n", 200), nil, now),
		alert("3", "Fine", nil, now),
	}}, time.Hour)
	if len(got) != 3 || got[0].Key != "Edge/"+strings.Repeat("f", 64) || got[2].Key != "Fine/3" {
		t.Fatalf("%d signals: %+v", len(got), got)
	}
}

func TestAKeyThatIsTooLongOrWouldBeRedactedIsDroppedFromTheDetails(t *testing.T) {
	a := alert("1", "A", map[string]string{"ok": "v", strings.Repeat("k", 65): "v", "AKIAIOSFODNN7EXAMPLE": "v"}, now)
	a.Annotations["fine"] = "x"
	a.Annotations[strings.Repeat("a", 65)] = "x"
	a.Annotations["password=hunter2hunter2"] = "x"
	got := fetch(t, &lister{alerts: []alertmanager.Alert{a}}, time.Hour)[0]
	var d struct {
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
		Truncated   bool              `json:"truncated"`
	}
	if err := json.Unmarshal(got.Details, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Labels) != 2 || d.Labels["ok"] != "v" || d.Labels["alertname"] != "A" || len(d.Annotations) != 1 || d.Annotations["fine"] != "x" || !d.Truncated {
		t.Fatalf("details = %s", got.Details)
	}
	if len(got.Labels) != 4 {
		t.Errorf("the full labels were cut: %d, want 4", len(got.Labels))
	}
}

func TestASourceWithoutAClientIsOffAndFailsWithoutPanicking(t *testing.T) {
	s := &signals.Alertmanager{On: func() bool { return true }}
	if s.Enabled() {
		t.Error("a source without a client is enabled")
	}
	if _, err := s.Fetch(context.Background()); err == nil {
		t.Error("Fetch without a client did not fail")
	}
}

func TestWithoutMaxAgeAnAlertIsYoungForAnHour(t *testing.T) {
	s := &signals.Alertmanager{
		Client: &lister{alerts: []alertmanager.Alert{
			alert("a", "A", map[string]string{"severity": "critical"}, now.Add(-30*time.Minute)),
			alert("b", "B", map[string]string{"severity": "critical"}, now.Add(-2*time.Hour)),
		}},
		On: func() bool { return true }, Now: func() time.Time { return now },
	}
	got, err := s.Fetch(context.Background())
	if err != nil || len(got) != 2 || !got[0].Auto || got[1].Auto {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestAFailedListingIsAnError(t *testing.T) {
	boom := errors.New("down")
	if _, err := source(&lister{err: boom}, time.Hour).Fetch(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("error = %v", err)
	}
}
