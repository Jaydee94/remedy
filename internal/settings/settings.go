package settings

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

// Source says where a value comes from.
type Source string

const (
	SourceDefault     Source = "default"
	SourceEnvironment Source = "environment"
	SourceSet         Source = "set here"
)

// Entry is one setting as it applies now.
type Entry struct {
	Spec     Spec
	Value    Value
	Source   Source
	Fallback Source // the source the value has after a reset: the environment if it set one, else the default
	Problem  string // a stored value that cannot be used; the value is then the environment's or the default
	Hint     string // a secret's last characters (none for a short one); empty for every other kind
}

// ErrUnknownKey means the key is not in the registry.
var ErrUnknownKey = errors.New("settings: unknown key")

var (
	// ErrNotSet means a secret has no value. ErrCannotOpen means it has one that cannot be opened (a changed master key, a
	// damaged value, or a reader without a key). ErrNoKey means a secret cannot be stored because the reader has no master key.
	ErrNotSet     = errors.New("settings: the secret is not set")
	ErrCannotOpen = errors.New("settings: the stored secret cannot be opened")
	ErrNoKey      = errors.New("settings: no master key to seal a secret with")
)

// InvalidError is a value that the registry refuses. Message is fit to show.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return e.Message }

// snapshot holds the sealed bytes of a secret and never its plaintext.
type snapshot struct {
	byKey  map[string]Entry
	sealed map[string][]byte
}

// Settings answers every component from a snapshot in memory, so reading never touches the database (the store has one
// connection, and some readers run inside a transaction). The snapshot is rebuilt after every committed write. The control plane
// is one process, so it is never behind the table.
type Settings struct {
	store *store.Store
	env   Layer
	log   *slog.Logger
	mu    sync.Mutex // serialises Set and Delete together with the rebuild
	snap  atomic.Pointer[snapshot]

	key     secret.Key // the master key that seals the secrets; only valid when haveKey
	haveKey bool

	afterCommit func() // a test seam: called after a store write succeeded and before the reload; nil in production
}

// reloadCommitted rebuilds the snapshot after a committed write. It does not use the request's context: a client that
// disconnects (or a server that shuts down) between the commit and the rebuild must not leave the snapshot behind the table.
func (s *Settings) reloadCommitted(ctx context.Context) error {
	if s.afterCommit != nil {
		s.afterCommit()
	}
	return s.reload(context.WithoutCancel(ctx))
}

// Option configures a Settings reader.
type Option func(*Settings)

// WithKey gives the reader the master key that seals and opens secret settings. Without it a secret can be neither stored nor opened.
func WithKey(k secret.Key) Option {
	return func(s *Settings) { s.key, s.haveKey = k, true }
}

// New reads the table once. It never fails: when the table cannot be read, that is logged and the environment and the defaults
// apply until the next successful write.
func New(ctx context.Context, st *store.Store, env Layer, log *slog.Logger, opts ...Option) *Settings {
	s := &Settings{store: st, env: env, log: log}
	for _, o := range opts {
		o(s)
	}
	if err := s.reload(ctx); err != nil {
		log.Error("cannot read the settings table; the environment and the defaults apply", "err", err)
		s.snap.Store(s.build(nil))
	}
	return s
}

func (s *Settings) reload(ctx context.Context) error {
	rows, err := s.store.ListSettings(ctx)
	if err != nil {
		return err
	}
	s.snap.Store(s.build(rows))
	return nil
}

// lower is the value a key has without an override.
func (s *Settings) lower(sp Spec) (Value, Source) {
	if v, ok := s.env[sp.Key]; ok {
		return v, SourceEnvironment
	}
	return sp.Default, SourceDefault
}

func (s *Settings) build(rows []store.Setting) *snapshot {
	stored := map[string]string{}
	for _, r := range rows {
		stored[r.Key] = r.Value
	}
	snap := &snapshot{byKey: map[string]Entry{}, sealed: map[string][]byte{}}
	for _, sp := range specs {
		if sp.Kind == KindSecret {
			e := Entry{Spec: sp, Source: SourceDefault, Fallback: SourceDefault}
			if raw, ok := stored[sp.Key]; ok {
				s.buildSecret(&e, snap, raw)
				delete(stored, sp.Key)
			}
			snap.byKey[sp.Key] = e
			continue
		}
		v, src := s.lower(sp)
		e := Entry{Spec: sp, Value: v, Source: src, Fallback: src}
		if raw, ok := stored[sp.Key]; ok {
			if sv, err := sp.ParseJSON(json.RawMessage(raw)); err != nil {
				e.Problem = fmt.Sprintf("The stored value cannot be used (%s). The %s applies.", err, src)
				s.log.Warn("ignoring a stored setting that is no longer valid", "key", sp.Key, "err", err)
			} else {
				e.Value, e.Source = sv, SourceSet
			}
			delete(stored, sp.Key)
		}
		snap.byKey[sp.Key] = e
	}
	for key := range stored {
		s.log.Warn("ignoring a stored setting this version does not know", "key", key)
	}
	return snap
}

// storedSecret is the stored form of a secret: the sealed token and a hint that shows its last characters.
type storedSecret struct {
	Sealed string `json:"sealed"`
	Hint   string `json:"hint"`
}

func aadFor(key string) string { return "settings/" + key }

// buildSecret fills the entry of a stored secret. It keeps the sealed bytes in the snapshot and checks that the master key opens
// them, but it keeps no plaintext. No message here contains the token.
func (s *Settings) buildSecret(e *Entry, snap *snapshot, raw string) {
	var st storedSecret
	var sealed []byte
	err := json.Unmarshal([]byte(raw), &st)
	if err == nil {
		sealed, err = base64.StdEncoding.DecodeString(st.Sealed)
	}
	if err != nil || len(sealed) == 0 {
		e.Problem = "The stored token cannot be used. Enter it again."
		s.log.Warn("ignoring a stored secret that is no longer valid", "key", e.Spec.Key)
		return
	}
	snap.sealed[e.Spec.Key] = sealed
	e.Source, e.Hint = SourceSet, st.Hint
	if !s.haveKey {
		e.Problem = "No master key is available, so the stored token cannot be opened."
		return
	}
	if _, err := s.key.Open(sealed, aadFor(e.Spec.Key)); err != nil {
		e.Problem = "The stored token cannot be decrypted. Check REMEDY_MASTER_KEY or enter the token again."
		s.log.Warn("a stored secret cannot be opened", "key", e.Spec.Key)
	}
}

// Secret opens a secret setting. It never touches the database; the sealed bytes are in the snapshot.
func (s *Settings) Secret(key string) (secret.Value, error) {
	if e := s.must(key); e.Spec.Kind != KindSecret {
		panic("settings: " + key + " is not a secret")
	}
	sealed, ok := s.snap.Load().sealed[key]
	if !ok {
		return secret.Value{}, ErrNotSet
	}
	if !s.haveKey {
		return secret.Value{}, ErrCannotOpen
	}
	plain, err := s.key.Open(sealed, aadFor(key))
	if err != nil {
		return secret.Value{}, ErrCannotOpen
	}
	return secret.NewValue(string(plain)), nil
}

// Entry returns one setting.
func (s *Settings) Entry(key string) (Entry, bool) {
	e, ok := s.snap.Load().byKey[key]
	return e, ok
}

func (s *Settings) must(key string) Entry {
	e, ok := s.Entry(key)
	if !ok {
		panic("settings: unknown key " + key)
	}
	return e
}

// Duration is the value of a duration setting.
func (s *Settings) Duration(key string) time.Duration { return s.must(key).Value.Dur }

// Int is the value of a number setting.
func (s *Settings) Int(key string) int { return s.must(key).Value.Num }

// List is the value of a list setting. The caller owns the slice.
func (s *Settings) List(key string) []string { return slices.Clone(s.must(key).Value.List) }

// Text is the value of a text setting.
func (s *Settings) Text(key string) string { return s.must(key).Value.Str }

// Entries returns every setting in the order of the registry.
func (s *Settings) Entries() []Entry {
	snap := s.snap.Load()
	out := make([]Entry, 0, len(specs))
	for _, sp := range specs {
		out = append(out, snap.byKey[sp.Key])
	}
	return out
}

// DiagnosisLimits are the three limits of automatic diagnosis as the store checks them.
func (s *Settings) DiagnosisLimits() store.DiagnosisLimits {
	return store.DiagnosisLimits{
		Cooldown:       s.Duration(DiagnoseCooldown),
		MaxPerIncident: s.Int(DiagnoseMaxPerIncident),
		MaxPerDay:      s.Int(DiagnoseMaxPerDay),
	}
}

func (s *Settings) activity(sp Spec, verb string, from Value, fromSrc Source, to Value, toSrc Source) store.NewActivity {
	data, _ := json.Marshal(map[string]string{
		"key": sp.Key, "from": from.Text(sp.Kind), "fromSource": string(fromSrc), "to": to.Text(sp.Kind), "toSource": string(toSrc),
	})
	return store.NewActivity{
		Kind:    store.KindSettingChanged,
		Summary: fmt.Sprintf("Setting %s %s from %s (%s) to %s (%s)", sp.Key, verb, from.Text(sp.Kind), fromSrc, to.Text(sp.Kind), toSrc),
		Data:    data,
	}
}

// Set checks a value against the registry, stores it with its activity entry and rebuilds the snapshot.
func (s *Settings) Set(ctx context.Context, key string, raw json.RawMessage) (Entry, error) {
	sp, ok := Lookup(key)
	if !ok {
		return Entry{}, ErrUnknownKey
	}
	v, err := sp.ParseJSON(raw)
	if err != nil {
		return Entry{}, &InvalidError{Message: key + " " + err.Error()}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sp.Kind == KindSecret {
		return s.setSecret(ctx, sp, v.Str)
	}
	old := s.must(key)
	act := s.activity(sp, "changed", old.Value, old.Source, v, SourceSet)
	if err := s.store.SetSetting(ctx, key, string(v.JSON(sp.Kind)), act); err != nil {
		return Entry{}, err
	}
	if err := s.reloadCommitted(ctx); err != nil {
		return Entry{}, err
	}
	return s.must(key), nil
}

// setSecret seals a token and stores it with an activity entry that names the key only. The caller holds s.mu.
func (s *Settings) setSecret(ctx context.Context, sp Spec, plain string) (Entry, error) {
	if !s.haveKey {
		return Entry{}, ErrNoKey
	}
	sealed, err := s.key.Seal([]byte(plain), aadFor(sp.Key))
	if err != nil {
		return Entry{}, err
	}
	b, _ := json.Marshal(storedSecret{Sealed: base64.StdEncoding.EncodeToString(sealed), Hint: hintOf(plain)})
	if err := s.store.SetSetting(ctx, sp.Key, string(b), secretActivity(sp, "changed")); err != nil {
		return Entry{}, err
	}
	if err := s.reloadCommitted(ctx); err != nil {
		return Entry{}, err
	}
	return s.must(sp.Key), nil
}

// secretActivity names the key and never a value.
func secretActivity(sp Spec, verb string) store.NewActivity {
	data, _ := json.Marshal(map[string]string{"key": sp.Key})
	return store.NewActivity{Kind: store.KindSettingChanged, Summary: "Setting " + sp.Key + " " + verb, Data: data}
}

// hintOf shows the last four characters of a long token and nothing of a short one.
func hintOf(plain string) string {
	r := []rune(plain)
	if len(r) < 12 {
		return "…"
	}
	return "…" + string(r[len(r)-4:])
}

// Delete removes the override of a key. Without one it changes nothing.
func (s *Settings) Delete(ctx context.Context, key string) (Entry, error) {
	sp, ok := Lookup(key)
	if !ok {
		return Entry{}, ErrUnknownKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.must(key)
	to, toSrc := s.lower(sp)
	act := s.activity(sp, "reset", old.Value, old.Source, to, toSrc)
	if sp.Kind == KindSecret {
		act = secretActivity(sp, "removed")
	}
	removed, err := s.store.DeleteSetting(ctx, key, act)
	if err != nil {
		return Entry{}, err
	}
	if removed {
		if err := s.reloadCommitted(ctx); err != nil {
			return Entry{}, err
		}
	}
	return s.must(key), nil
}
