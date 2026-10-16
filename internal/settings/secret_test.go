package settings_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
)

const plainToken = "tok-0123456789abcdef-SECRET"

func key(t *testing.T, fill byte) secret.Key {
	t.Helper()
	k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newKeyed(t *testing.T, k secret.Key, logTo io.Writer) (*settings.Settings, *store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return settings.New(context.Background(), st, nil, slog.New(slog.NewTextHandler(logTo, nil)), settings.WithKey(k)), st, path
}

func TestASecretIsSealedAndOpenedOnDemand(t *testing.T) {
	var logs bytes.Buffer
	s, st, path := newKeyed(t, key(t, 5), &logs)
	ctx := context.Background()

	if _, err := s.Secret(settings.AlertmanagerToken); !errors.Is(err, settings.ErrNotSet) {
		t.Fatalf("before: %v", err)
	}
	e, err := s.Set(ctx, settings.AlertmanagerToken, json.RawMessage(`"`+plainToken+`"`))
	if err != nil || e.Source != settings.SourceSet || e.Hint != "…CRET" {
		t.Fatalf("entry = %+v, %v", e, err)
	}
	got, err := s.Secret(settings.AlertmanagerToken)
	if err != nil || got.Reveal() != plainToken {
		t.Fatalf("Secret = %q, %v", got.Reveal(), err)
	}
	if got.String() != "***" {
		t.Error("the secret value prints its content")
	}

	// A token never reaches the activity, the logs, the entries or the database file.
	log, _ := st.ListActivity(ctx, store.ActivityQuery{})
	var everything strings.Builder
	for _, a := range log {
		everything.WriteString(a.Summary + string(a.Data))
	}
	for _, en := range s.Entries() {
		b, _ := json.Marshal(en.Value.JSON(en.Spec.Kind))
		everything.WriteString(string(b) + en.Hint + en.Problem)
	}
	everything.WriteString(logs.String())
	if strings.Contains(everything.String(), plainToken) || strings.Contains(everything.String(), "0123456789abcdef") {
		t.Fatalf("the token is in the activity, the entries or the logs:\n%s", everything.String())
	}
	for _, f := range []string{path, path + "-wal"} {
		if raw, err := os.ReadFile(f); err == nil && bytes.Contains(raw, []byte(plainToken)) {
			t.Fatalf("the token is in %s in plaintext", f)
		}
	}
	if len(log) != 1 || log[0].Summary != "Setting alertmanager_token changed" || string(log[0].Data) != `{"key":"alertmanager_token"}` {
		t.Fatalf("activity = %+v", log)
	}
}

func TestAShortTokenShowsNoCharactersOfItself(t *testing.T) {
	s, _, _ := newKeyed(t, key(t, 5), io.Discard)
	e, err := s.Set(context.Background(), settings.AlertmanagerToken, json.RawMessage(`"short-token"`))
	if err != nil || e.Hint != "…" {
		t.Fatalf("hint = %q, %v", e.Hint, err)
	}
}

func TestASecretIsRemovedWithoutAValueInTheActivity(t *testing.T) {
	s, st, _ := newKeyed(t, key(t, 5), io.Discard)
	ctx := context.Background()
	_, _ = s.Set(ctx, settings.AlertmanagerToken, json.RawMessage(`"`+plainToken+`"`))
	e, err := s.Delete(ctx, settings.AlertmanagerToken)
	if err != nil || e.Source != settings.SourceDefault || e.Hint != "" {
		t.Fatalf("entry = %+v, %v", e, err)
	}
	if _, err := s.Secret(settings.AlertmanagerToken); !errors.Is(err, settings.ErrNotSet) {
		t.Fatalf("after removal: %v", err)
	}
	got := activityTexts(t, st) // newest first
	if len(got) != 2 || got[0] != "Setting alertmanager_token removed" {
		t.Fatalf("activity = %q", got)
	}
}

func TestASecretThatTheMasterKeyDoesNotOpenIsAProblem(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := settings.New(ctx, st, nil, quiet, settings.WithKey(key(t, 5)))
	if _, err := first.Set(ctx, settings.AlertmanagerToken, json.RawMessage(`"`+plainToken+`"`)); err != nil {
		t.Fatal(err)
	}
	other := settings.New(ctx, st, nil, quiet, settings.WithKey(key(t, 6)))
	e, _ := other.Entry(settings.AlertmanagerToken)
	if e.Problem == "" || e.Source != settings.SourceSet || strings.Contains(e.Problem, plainToken) {
		t.Fatalf("entry = %+v", e)
	}
	if _, err := other.Secret(settings.AlertmanagerToken); !errors.Is(err, settings.ErrCannotOpen) {
		t.Fatalf("Secret with the wrong key: %v", err)
	}
	// A reader without any key cannot open it either, and cannot store one.
	none := settings.New(ctx, st, nil, quiet)
	if _, err := none.Secret(settings.AlertmanagerToken); !errors.Is(err, settings.ErrCannotOpen) {
		t.Fatalf("Secret without a key: %v", err)
	}
	if _, err := none.Set(ctx, settings.AlertmanagerToken, json.RawMessage(`"x"`)); !errors.Is(err, settings.ErrNoKey) {
		t.Fatalf("Set without a key: %v", err)
	}
}

func TestASecretValueIsCheckedAndNeverEchoed(t *testing.T) {
	s, _, _ := newKeyed(t, key(t, 5), io.Discard)
	for _, raw := range []string{`""`, `null`, `5`, `"leak-me\nplease"`, `"leak-me\u0001please"`, `"` + strings.Repeat("leak-me", 700) + `"`} {
		_, err := s.Set(context.Background(), settings.AlertmanagerToken, json.RawMessage(raw))
		var invalid *settings.InvalidError
		if !errors.As(err, &invalid) {
			t.Errorf("%.30s: error = %v", raw, err)
			continue
		}
		if strings.Contains(invalid.Message, "leak-me") {
			t.Errorf("the message echoes the value: %q", invalid.Message)
		}
	}
}

func TestAStoredSecretThatIsNotTheExpectedShapeIsAProblem(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.SetSetting(ctx, settings.AlertmanagerToken, `"just a string"`, store.NewActivity{Kind: store.KindSettingChanged, Summary: "x"})
	s := settings.New(ctx, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), settings.WithKey(key(t, 5)))
	e, _ := s.Entry(settings.AlertmanagerToken)
	if e.Problem == "" || e.Source != settings.SourceDefault {
		t.Fatalf("entry = %+v", e)
	}
}
