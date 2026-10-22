package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func newReader(t *testing.T, env settings.Layer) (*settings.Settings, *store.Store) {
	t.Helper()
	st := testutil.Store(t)
	return settings.New(context.Background(), st, env, slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func entry(t *testing.T, s *settings.Settings, key string) settings.Entry {
	t.Helper()
	e, ok := s.Entry(key)
	if !ok {
		t.Fatalf("no entry for %q", key)
	}
	return e
}

func TestTheValueComesFromTheDatabaseThenTheEnvironmentThenTheDefault(t *testing.T) {
	ctx := context.Background()
	s, _ := newReader(t, settings.Layer{settings.PollInterval: {Dur: 30 * time.Second}})

	if e := entry(t, s, settings.DiagnoseCooldown); e.Source != settings.SourceDefault || e.Value.Dur != 15*time.Minute || e.Fallback != settings.SourceDefault {
		t.Errorf("cooldown = %+v", e)
	}
	if e := entry(t, s, settings.PollInterval); e.Source != settings.SourceEnvironment || e.Value.Dur != 30*time.Second || e.Fallback != settings.SourceEnvironment {
		t.Errorf("poll = %+v", e)
	}
	e, err := s.Set(ctx, settings.PollInterval, json.RawMessage(`"20s"`))
	if err != nil || e.Source != settings.SourceSet || e.Value.Dur != 20*time.Second || e.Fallback != settings.SourceEnvironment {
		t.Fatalf("after Set: %+v, %v", e, err)
	}
	if s.Duration(settings.PollInterval) != 20*time.Second {
		t.Errorf("Duration = %v", s.Duration(settings.PollInterval))
	}
	e, err = s.Delete(ctx, settings.PollInterval)
	if err != nil || e.Source != settings.SourceEnvironment || e.Value.Dur != 30*time.Second {
		t.Fatalf("after Delete: %+v, %v", e, err)
	}
}

func TestTheGettersAnswerForEveryKind(t *testing.T) {
	ctx := context.Background()
	s, _ := newReader(t, nil)
	if _, err := s.Set(ctx, settings.K8sWriteNamespaces, json.RawMessage(`["demo","staging"]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, settings.DiagnoseMaxPerDay, json.RawMessage(`7`)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.List(settings.K8sWriteNamespaces), []string{"demo", "staging"}) || s.Int(settings.DiagnoseMaxPerDay) != 7 {
		t.Fatalf("list = %v, int = %d", s.List(settings.K8sWriteNamespaces), s.Int(settings.DiagnoseMaxPerDay))
	}
	got := s.List(settings.K8sWriteNamespaces)
	got[0] = "changed"
	if s.List(settings.K8sWriteNamespaces)[0] != "demo" {
		t.Error("List hands out the snapshot's own slice")
	}
	limits := s.DiagnosisLimits()
	if limits.MaxPerDay != 7 || limits.MaxPerIncident != 3 || limits.Cooldown != 15*time.Minute {
		t.Errorf("limits = %+v", limits)
	}
}

func TestThePollIntervalPacesGitHubOnly(t *testing.T) {
	for _, sp := range settings.Specs() {
		if sp.Key != settings.PollInterval {
			continue
		}
		if sp.Label != "How often I check GitHub" || sp.Rule != "At least 10s, for example 60s or 2m." {
			t.Fatalf("label %q, rule %q", sp.Label, sp.Rule)
		}
		return
	}
	t.Fatal("no poll interval spec")
}

func TestEntriesComeInTheOrderOfTheRegistry(t *testing.T) {
	s, _ := newReader(t, nil)
	var got, want []string
	for _, e := range s.Entries() {
		got = append(got, e.Spec.Key)
	}
	for _, sp := range settings.Specs() {
		want = append(want, sp.Key)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestAValueOutsideTheBoundsChangesNothing(t *testing.T) {
	ctx := context.Background()
	s, st := newReader(t, nil)
	_, err := s.Set(ctx, settings.PollInterval, json.RawMessage(`"5s"`))
	var invalid *settings.InvalidError
	if !errors.As(err, &invalid) || !strings.HasPrefix(invalid.Message, "poll_interval must be a duration of at least 10s") {
		t.Fatalf("error = %v", err)
	}
	if s.Duration(settings.PollInterval) != time.Minute {
		t.Errorf("the snapshot changed: %v", s.Duration(settings.PollInterval))
	}
	if rows, _ := st.ListSettings(ctx); len(rows) != 0 {
		t.Errorf("a row was written: %+v", rows)
	}
	if got := activityTexts(t, st); len(got) != 0 {
		t.Errorf("an activity entry was written: %v", got)
	}
	if _, err := s.Set(ctx, "no_such_key", json.RawMessage(`1`)); !errors.Is(err, settings.ErrUnknownKey) {
		t.Errorf("unknown key: %v", err)
	}
	if _, err := s.Delete(ctx, "no_such_key"); !errors.Is(err, settings.ErrUnknownKey) {
		t.Errorf("unknown key on delete: %v", err)
	}
}

func activityTexts(t *testing.T, st *store.Store) []string {
	t.Helper()
	log, err := st.ListActivity(context.Background(), store.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range log {
		out = append(out, a.Summary)
	}
	return out
}

func TestEveryChangeIsAnActivityEntryWithTheOldAndTheNewValue(t *testing.T) {
	ctx := context.Background()
	s, st := newReader(t, nil)
	_, _ = s.Set(ctx, settings.PollInterval, json.RawMessage(`"30s"`))
	_, _ = s.Delete(ctx, settings.PollInterval)
	_, _ = s.Delete(ctx, settings.PollInterval) // nothing to reset: no entry

	got := activityTexts(t, st) // newest first
	if len(got) != 2 ||
		got[1] != "Setting poll_interval changed from 1m (default) to 30s (set here)" ||
		got[0] != "Setting poll_interval reset from 30s (set here) to 1m (default)" {
		t.Fatalf("activity = %q", got)
	}
}

func TestAStoredValueThatIsNoLongerValidIsIgnoredAndReported(t *testing.T) {
	ctx := context.Background()
	st := testutil.Store(t)
	act := store.NewActivity{Kind: store.KindSettingChanged, Summary: "x"}
	_ = st.SetSetting(ctx, settings.PollInterval, `"5s"`, act)        // below the bound
	_ = st.SetSetting(ctx, "a_key_from_the_future", `1`, act)         // not in the registry
	_ = st.SetSetting(ctx, settings.DiagnoseMaxPerDay, `"many"`, act) // the wrong type

	s := settings.New(ctx, st, settings.Layer{settings.PollInterval: {Dur: 40 * time.Second}}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	e := entry(t, s, settings.PollInterval)
	if e.Value.Dur != 40*time.Second || e.Source != settings.SourceEnvironment || e.Problem == "" {
		t.Errorf("poll = %+v: the environment's value must stay, with a problem", e)
	}
	if e := entry(t, s, settings.DiagnoseMaxPerDay); e.Value.Num != 20 || e.Source != settings.SourceDefault || e.Problem == "" {
		t.Errorf("per day = %+v", e)
	}
	if e := entry(t, s, settings.RunTimeout); e.Problem != "" {
		t.Errorf("run timeout has a problem: %+v", e)
	}
	// A good value replaces the bad row and clears the problem.
	if e, err := s.Set(ctx, settings.PollInterval, json.RawMessage(`"20s"`)); err != nil || e.Problem != "" || e.Source != settings.SourceSet {
		t.Errorf("after a good Set: %+v, %v", e, err)
	}
}

func TestAFailedWriteLeavesTheSnapshotAsItWas(t *testing.T) {
	s, st := newReader(t, nil)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(context.Background(), settings.PollInterval, json.RawMessage(`"20s"`)); err == nil {
		t.Fatal("a write to a closed store succeeded")
	}
	if s.Duration(settings.PollInterval) != time.Minute {
		t.Errorf("the snapshot changed to %v", s.Duration(settings.PollInterval))
	}
}

func TestAnUnreadableTableMeansTheEnvironmentAndTheDefaults(t *testing.T) {
	st := testutil.Store(t)
	_ = st.Close()
	s := settings.New(context.Background(), st, settings.Layer{settings.RunTimeout: {Dur: 20 * time.Minute}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if s.Duration(settings.RunTimeout) != 20*time.Minute || s.Duration(settings.PollInterval) != time.Minute {
		t.Fatalf("run timeout = %v, poll = %v", s.Duration(settings.RunTimeout), s.Duration(settings.PollInterval))
	}
}

func TestReadingWhileWritingIsSafe(t *testing.T) {
	ctx := context.Background()
	s, _ := newReader(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = s.Duration(settings.PollInterval)
				_ = s.List(settings.K8sWriteNamespaces)
				_ = s.Entries()
			}
		}()
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := s.Set(ctx, settings.DiagnoseMaxPerDay, json.RawMessage(strconv.Itoa(i+j))); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestAnUnknownKeyInAGetterIsAProgrammingError(t *testing.T) {
	s, _ := newReader(t, nil)
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	s.Duration("poll_intervall")
}

func TestATextSettingIsSetAndReadBack(t *testing.T) {
	ctx := context.Background()
	s, st := newReader(t, nil)
	if s.Text(settings.AlertmanagerURL) != "" {
		t.Fatal("the default address is not empty")
	}
	e, err := s.Set(ctx, settings.AlertmanagerURL, json.RawMessage(`"http://am:9093/"`))
	if err != nil || e.Source != settings.SourceSet || s.Text(settings.AlertmanagerURL) != "http://am:9093" {
		t.Fatalf("%+v, %v", e, err)
	}
	if got := activityTexts(t, st); len(got) != 1 || got[0] != "Setting alertmanager_url changed from none (default) to http://am:9093 (set here)" {
		t.Fatalf("activity = %q", got)
	}
	if _, err := s.Set(ctx, settings.AlertmanagerURL, json.RawMessage(`"http://u:p@am"`)); err == nil {
		t.Error("an address with a user name was accepted")
	}
}
