package settings_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/settings"
)

func spec(t *testing.T, key string) settings.Spec {
	t.Helper()
	s, ok := settings.Lookup(key)
	if !ok {
		t.Fatalf("no spec for %q", key)
	}
	return s
}

func TestEveryDefaultPassesItsOwnBoundsAndSurvivesTheJSONRoundTrip(t *testing.T) {
	for _, s := range settings.Specs() {
		v, err := s.ParseJSON(s.Default.JSON(s.Kind))
		if err != nil {
			t.Errorf("%s: the default %s does not parse: %v", s.Key, s.Default.JSON(s.Kind), err)
			continue
		}
		if v.Text(s.Kind) != s.Default.Text(s.Kind) {
			t.Errorf("%s: round trip %q, want %q", s.Key, v.Text(s.Kind), s.Default.Text(s.Kind))
		}
		if s.Env == "" || s.Label == "" || s.Group == "" || s.Rule == "" || s.Effect == "" {
			t.Errorf("%s: a field of the spec is empty: %+v", s.Key, s)
		}
	}
}

func TestTheTextOfAValueIsCheckedAgainstTheBounds(t *testing.T) {
	cases := []struct {
		key, text string
		ok        bool
	}{
		{settings.PollInterval, "10s", true}, {settings.PollInterval, "2m", true}, {settings.PollInterval, "9s", false},
		{settings.PollInterval, "abc", false}, {settings.PollInterval, "60", false},
		{settings.DiagnoseCooldown, "1m", true}, {settings.DiagnoseCooldown, "59s", false},
		{settings.DiagnoseMaxPerIncident, "0", true}, {settings.DiagnoseMaxPerIncident, "20", true},
		{settings.DiagnoseMaxPerIncident, "21", false}, {settings.DiagnoseMaxPerIncident, "-1", false}, {settings.DiagnoseMaxPerIncident, "x", false},
		{settings.DiagnoseMaxPerDay, "0", true}, {settings.DiagnoseMaxPerDay, "200", true}, {settings.DiagnoseMaxPerDay, "201", false},
		{settings.RunTimeout, "10s", true}, {settings.RunTimeout, "9s", false}, {settings.RunTimeout, "1h", true},
		{settings.K8sWriteNamespaces, "demo,staging", true}, {settings.K8sWriteNamespaces, "", true}, {settings.K8sWriteNamespaces, "Not A Name", false},
	}
	for _, c := range cases {
		if _, err := spec(t, c.key).ParseText(c.text); (err == nil) != c.ok {
			t.Errorf("%s %q: error = %v, want ok = %v", c.key, c.text, err, c.ok)
		}
	}
}

func TestTheListDropsDuplicatesAndEmptyEntries(t *testing.T) {
	v, err := spec(t, settings.K8sWriteNamespaces).ParseText("demo, staging,,demo")
	if err != nil || !slices.Equal(v.List, []string{"demo", "staging"}) {
		t.Fatalf("list = %v, %v", v.List, err)
	}
}

func TestTheJSONFormIsAStringForADurationANumberAndAnArray(t *testing.T) {
	good := []struct {
		key, raw string
		check    func(settings.Value) bool
	}{
		{settings.PollInterval, `"30s"`, func(v settings.Value) bool { return v.Dur == 30*time.Second }},
		{settings.DiagnoseMaxPerDay, `5`, func(v settings.Value) bool { return v.Num == 5 }},
		{settings.K8sWriteNamespaces, `["demo","staging"]`, func(v settings.Value) bool { return slices.Equal(v.List, []string{"demo", "staging"}) }},
	}
	for _, c := range good {
		v, err := spec(t, c.key).ParseJSON(json.RawMessage(c.raw))
		if err != nil || !c.check(v) {
			t.Errorf("%s %s: %+v, %v", c.key, c.raw, v, err)
		}
	}
	for _, c := range []struct{ key, raw string }{
		{settings.PollInterval, `900`}, {settings.PollInterval, `"soon"`}, {settings.DiagnoseMaxPerDay, `"5"`}, {settings.DiagnoseMaxPerDay, `3.5`},
		{settings.K8sWriteNamespaces, `"demo"`}, {settings.K8sWriteNamespaces, `[1]`}, {settings.PollInterval, `null`}, {settings.DiagnoseMaxPerDay, ``},
		{settings.DiagnoseMaxPerDay, `null`}, {settings.K8sWriteNamespaces, `null`},
	} {
		if _, err := spec(t, c.key).ParseJSON(json.RawMessage(c.raw)); err == nil {
			t.Errorf("%s %s was accepted", c.key, c.raw)
		}
	}
}

func TestADurationIsWrittenTheWayAPersonWritesIt(t *testing.T) {
	for d, want := range map[time.Duration]string{90 * time.Second: `"90s"`, 15 * time.Minute: `"15m"`, time.Hour: `"1h"`, 61 * time.Second: `"1m1s"`} {
		if got := string((settings.Value{Dur: d}).JSON(settings.KindDuration)); got != want {
			t.Errorf("%s = %s, want %s", d, got, want)
		}
	}
	if got := string((settings.Value{List: []string{"a", "b"}}).JSON(settings.KindList)); got != `["a","b"]` {
		t.Errorf("list = %s", got)
	}
	if got := string((settings.Value{}).JSON(settings.KindList)); got != `[]` {
		t.Errorf("empty list = %s, want []", got)
	}
	if got := (settings.Value{}).Text(settings.KindList); got != "none" {
		t.Errorf("empty list text = %q", got)
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestTheEnvironmentLayerHoldsOnlyWhatWasSet(t *testing.T) {
	l, err := settings.EnvLayer(env(map[string]string{"REMEDY_POLL_INTERVAL": "30s", "REMEDY_K8S_WRITE_NAMESPACES": "demo"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l[settings.DiagnoseCooldown]; ok || len(l) != 2 {
		t.Fatalf("layer = %v", l)
	}
	if l.Duration(settings.PollInterval) != 30*time.Second || l.Duration(settings.DiagnoseCooldown) != 15*time.Minute {
		t.Errorf("poll = %v, cooldown = %v", l.Duration(settings.PollInterval), l.Duration(settings.DiagnoseCooldown))
	}
	if l.Int(settings.DiagnoseMaxPerDay) != 20 || !slices.Equal(l.List(settings.K8sWriteNamespaces), []string{"demo"}) {
		t.Errorf("per day = %d, namespaces = %v", l.Int(settings.DiagnoseMaxPerDay), l.List(settings.K8sWriteNamespaces))
	}
}

func TestAnInvalidEnvironmentValueIsRefusedWithTheTextTheEnvironmentAlwaysHad(t *testing.T) {
	for name, want := range map[string]string{
		"REMEDY_POLL_INTERVAL":             "REMEDY_POLL_INTERVAL must be a duration of at least 10s, for example 60s or 2m",
		"REMEDY_DIAGNOSE_COOLDOWN":         "REMEDY_DIAGNOSE_COOLDOWN must be a duration of at least 1m, for example 15m",
		"REMEDY_DIAGNOSE_MAX_PER_INCIDENT": "REMEDY_DIAGNOSE_MAX_PER_INCIDENT must be a whole number from 0 to 20",
		"REMEDY_DIAGNOSE_MAX_PER_DAY":      "REMEDY_DIAGNOSE_MAX_PER_DAY must be a whole number from 0 to 200",
		"REMEDY_RUN_TIMEOUT":               "REMEDY_RUN_TIMEOUT must be a duration of at least 10s, for example 10m",
	} {
		_, err := settings.EnvLayer(env(map[string]string{name: "nonsense"}))
		if err == nil || err.Error() != want {
			t.Errorf("%s: error = %v, want %q", name, err, want)
		}
	}
	_, err := settings.EnvLayer(env(map[string]string{"REMEDY_K8S_WRITE_NAMESPACES": "Not A Name"}))
	if err == nil || !strings.HasPrefix(err.Error(), "REMEDY_K8S_WRITE_NAMESPACES: ") {
		t.Errorf("namespaces: error = %v", err)
	}
}

func TestEnvValueReadsOneKey(t *testing.T) {
	v, set, err := settings.EnvValue(settings.RunTimeout, env(map[string]string{"REMEDY_RUN_TIMEOUT": "20m", "REMEDY_POLL_INTERVAL": "nonsense"}))
	if err != nil || !set || v.Dur != 20*time.Minute {
		t.Fatalf("%+v %v %v: another key's mistake must not matter", v, set, err)
	}
	if _, set, err := settings.EnvValue(settings.RunTimeout, env(nil)); err != nil || set {
		t.Fatalf("unset: set = %v, err = %v", set, err)
	}
}
