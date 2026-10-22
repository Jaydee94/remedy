package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

// A flapping alert writes many entries; the opening line and the latest diagnosis entries must stay in the history even when
// they are older than the latest 100.
func TestGetIncidentKeepsTheOpeningAndTheDiagnosisOutsideTheWindow(t *testing.T) {
	e, repoID := withRepo(t)
	ctx := context.Background()
	in := openIncident(t, e, repoID, "pr:7", "go")
	add := func(kind, summary string) {
		t.Helper()
		if err := e.store.AddActivity(ctx, store.NewActivity{Kind: kind, RepoID: repoID, IncidentID: in.ID, Summary: summary}); err != nil {
			t.Fatal(err)
		}
	}
	add(store.KindDiagnosisStarted, "started")
	add(store.KindDiagnosisFinished, "finished")
	for i := 0; i < 120; i++ {
		add(store.KindIncidentResolved, "cleared")
	}

	type history struct {
		Activity []struct {
			ID   int64  `json:"id"`
			Kind string `json:"kind"`
		} `json:"activity"`
	}
	fetch := func(id int64) history {
		t.Helper()
		code, body := e.call(t, http.MethodGet, "/api/incidents/"+strconv.FormatInt(id, 10), "")
		if code != http.StatusOK {
			t.Fatalf("GET = %d %s", code, body)
		}
		var got history
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	got := fetch(in.ID)
	seen := map[int64]bool{}
	counts := map[string]int{}
	for i, a := range got.Activity {
		if seen[a.ID] {
			t.Fatalf("entry %d twice", a.ID)
		}
		seen[a.ID] = true
		if i > 0 && a.ID >= got.Activity[i-1].ID {
			t.Fatalf("entries are not newest first at %d", i)
		}
		counts[a.Kind]++
	}
	if counts[store.KindIncidentOpened] != 1 || counts[store.KindDiagnosisStarted] != 1 || counts[store.KindDiagnosisFinished] != 1 {
		t.Fatalf("kinds = %v, want the opening and the diagnosis entries outside the window of 100", counts)
	}
	if len(got.Activity) != 100+3 {
		t.Fatalf("%d entries, want the window of 100 and the 3 anchors", len(got.Activity))
	}

	// A short history has no duplicate.
	short := openIncident(t, e, repoID, "pr:8", "go")
	if got := fetch(short.ID); len(got.Activity) != 1 {
		t.Fatalf("short history = %+v, want one entry", got.Activity)
	}
}
