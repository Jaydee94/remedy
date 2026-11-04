package signals_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/signals"
)

// The webhook source bounds a sender's text with the Alertmanager source's own helpers: one definition of "bounded alert text".
func TestTheSharedHelpersBoundAndCleanText(t *testing.T) {
	if got := signals.OneLine("a \n\t b\x00c  "); got != "a b c" {
		t.Errorf("OneLine = %q", got)
	}
	if got, cut := signals.Cut("héllo", 3); got != "hél" || !cut {
		t.Errorf("Cut = %q, %v", got, cut)
	}
	if got := signals.CleanURL("javascript:alert(1)"); got != "" {
		t.Errorf("CleanURL kept a script address: %q", got)
	}
	if got := signals.CleanURL("https://x.example/a"); got != "https://x.example/a" {
		t.Errorf("CleanURL = %q", got)
	}
	if got := signals.CleanURL("https://x.example/" + strings.Repeat("a", 3000)); len([]rune(got)) != signals.MaxURL {
		t.Errorf("CleanURL kept %d characters, want %d", len([]rune(got)), signals.MaxURL)
	}
	for in, want := range map[string]string{"CRITICAL": "critical", " warning ": "warning", "info": "info", "page": "none", "": "none"} {
		if got := signals.SeverityOf(in); got != want {
			t.Errorf("SeverityOf(%q) = %q, want %q", in, got, want)
		}
	}
	in := map[string]string{"b": "2", "a": "1", "password": "x", strings.Repeat("k", 65): "long key"}
	out, cut := signals.Bounded(in, 2, 10)
	if len(out) != 2 || out["a"] != "1" || out["b"] != "2" || !cut {
		t.Errorf("Bounded = %v, %v", out, cut)
	}
	if signals.MaxLabels != 30 || signals.MaxLabelKey != 64 || signals.MaxLabelValue != 300 || signals.MaxAnnotations != 20 ||
		signals.MaxAnnotation != 1000 || signals.MaxTitle != 200 || signals.MaxURL != 2000 {
		t.Error("a bound of the spec changed")
	}
}

func TestTheSharedHostMappingPicksTheFirstHostAndNamesTheOthers(t *testing.T) {
	hosts, skipped := signals.CompileHosts([]signals.HostRules{
		{ID: 2, Name: "b", Matchers: `instance="nas"`},
		{ID: 1, Name: "a", Matchers: `instance="nas"`},
		{ID: 3, Name: "broken", Matchers: `((`},
		{ID: 4, Name: "none", Matchers: ``},
	})
	if len(skipped) != 1 || skipped[0] != "broken" {
		t.Fatalf("skipped = %v", skipped)
	}
	id, also := signals.MapHost(hosts, map[string]string{"instance": "nas"})
	if id != 1 || len(also) != 1 || also[0] != "b" {
		t.Fatalf("MapHost = %d, %v", id, also)
	}
	if id, _ := signals.MapHost(hosts, map[string]string{"instance": "other"}); id != 0 {
		t.Errorf("MapHost matched %d for another instance", id)
	}
	var d map[string]any
	if err := json.Unmarshal(signals.WithAlso(json.RawMessage(`{"a":1}`), []string{"b"}), &d); err != nil || d["alsoMatches"] == nil {
		t.Errorf("WithAlso = %v, %v", d, err)
	}
}
