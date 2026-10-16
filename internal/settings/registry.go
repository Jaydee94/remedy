// Package settings holds the values the maintainer changes in the Setup page. A registry in the code says which keys exist and what
// each may hold; the environment is the default, a row in the table `settings` overrides it, and a Settings reader answers every
// component from an in-memory snapshot.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/kube"
)

// The keys.
const (
	PollInterval           = "poll_interval"
	DiagnoseCooldown       = "diagnose_cooldown"
	DiagnoseMaxPerIncident = "diagnose_max_per_incident"
	DiagnoseMaxPerDay      = "diagnose_max_per_day"
	K8sWriteNamespaces     = "k8s_write_namespaces"
	RunTimeout             = "run_timeout"
)

// Spec says what one setting is. problem is the sentence that follows the variable's name when a value is refused; check is the
// bounds of the value.
type Spec struct {
	Key     string
	Group   string // the heading on the Setup page
	Label   string
	Env     string // the environment variable that gives the default
	Kind    Kind
	Default Value
	Rule    string // the bounds, for the Setup page
	Effect  string // when a change takes effect, for the Setup page
	problem string
	check   func(Value) bool
}

var specs = []Spec{
	{
		Key: PollInterval, Group: "Polling", Label: "How often I check GitHub", Env: "REMEDY_POLL_INTERVAL",
		Kind: KindDuration, Default: Value{Dur: time.Minute}, Rule: "At least 10s, for example 60s or 2m.",
		Effect:  "Takes effect at the next check.",
		problem: "must be a duration of at least 10s, for example 60s or 2m",
		check:   func(v Value) bool { return v.Dur >= 10*time.Second },
	},
	{
		Key: DiagnoseCooldown, Group: "Diagnosis limits", Label: "Time between two automatic diagnoses of one incident", Env: "REMEDY_DIAGNOSE_COOLDOWN",
		Kind: KindDuration, Default: Value{Dur: 15 * time.Minute}, Rule: "At least 1m, for example 15m.",
		Effect:  "Takes effect at the next automatic diagnosis.",
		problem: "must be a duration of at least 1m, for example 15m",
		check:   func(v Value) bool { return v.Dur >= time.Minute },
	},
	{
		Key: DiagnoseMaxPerIncident, Group: "Diagnosis limits", Label: "Automatic diagnoses per incident", Env: "REMEDY_DIAGNOSE_MAX_PER_INCIDENT",
		Kind: KindNumber, Default: Value{Num: 3}, Rule: "A whole number from 0 to 20.",
		Effect:  "Takes effect at the next automatic diagnosis.",
		problem: "must be a whole number from 0 to 20",
		check:   func(v Value) bool { return v.Num >= 0 && v.Num <= 20 },
	},
	{
		Key: DiagnoseMaxPerDay, Group: "Diagnosis limits", Label: "Automatic diagnoses per 24 hours", Env: "REMEDY_DIAGNOSE_MAX_PER_DAY",
		Kind: KindNumber, Default: Value{Num: 20}, Rule: "A whole number from 0 to 200. 0 turns automatic diagnosis off.",
		Effect:  "Takes effect at the next automatic diagnosis.",
		problem: "must be a whole number from 0 to 200",
		check:   func(v Value) bool { return v.Num >= 0 && v.Num <= 200 },
	},
	{
		Key: K8sWriteNamespaces, Group: "Cluster actions", Label: "Namespaces where I may act", Env: "REMEDY_K8S_WRITE_NAMESPACES",
		Kind: KindList, Rule: "Namespace names, separated by commas. Empty: I refuse every action.",
		Effect: "Checked before an approval is asked for and again when the action runs, so a change counts for a call that already waits.",
	},
	{
		Key: RunTimeout, Group: "Runs", Label: "Longest a run may take", Env: "REMEDY_RUN_TIMEOUT",
		Kind: KindDuration, Default: Value{Dur: 10 * time.Minute}, Rule: "At least 10s, for example 10m. Time spent waiting for an approval does not count.",
		Effect:  "Takes effect for the next run that starts.",
		problem: "must be a duration of at least 10s, for example 10m",
		check:   func(v Value) bool { return v.Dur >= 10*time.Second },
	},
}

// Specs returns the registry in the order the Setup page shows it.
func Specs() []Spec { return append([]Spec(nil), specs...) }

// Lookup finds the spec of a key.
func Lookup(key string) (Spec, bool) {
	for _, s := range specs {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}

// ParseText reads the form of the environment and of the input field: "30s", "5", "demo,staging". The error says what is wrong
// and does not name the setting.
func (s Spec) ParseText(text string) (Value, error) {
	var v Value
	switch s.Kind {
	case KindDuration:
		d, err := time.ParseDuration(text)
		if err != nil {
			return Value{}, errors.New(s.problem)
		}
		v.Dur = d
	case KindNumber:
		n, err := strconv.Atoi(text)
		if err != nil {
			return Value{}, errors.New(s.problem)
		}
		v.Num = n
	default:
		list, err := kube.ParseNamespaces(text)
		if err != nil {
			return Value{}, err
		}
		v.List = list
	}
	if s.check != nil && !s.check(v) {
		return Value{}, errors.New(s.problem)
	}
	return v, nil
}

// ParseJSON reads the stored and transported form (see Value.JSON). JSON null is refused for every kind: it would otherwise
// decode to 0 or to an empty list and silently switch a limit off or clear the allowlist.
func (s Spec) ParseJSON(raw json.RawMessage) (Value, error) {
	isNull := bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	switch s.Kind {
	case KindDuration:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil || isNull {
			return Value{}, fmt.Errorf("%s (write it as a string such as \"15m\")", s.problem)
		}
		return s.ParseText(strings.TrimSpace(text))
	case KindNumber:
		var n int
		if err := json.Unmarshal(raw, &n); err != nil || isNull {
			return Value{}, errors.New(s.problem)
		}
		return s.ParseText(strconv.Itoa(n))
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil || isNull {
		return Value{}, errors.New("must be a list of namespace names")
	}
	return s.ParseText(strings.Join(list, ","))
}

// envError names the variable in front of the sentence the way the configuration always did.
func (s Spec) envError(err error) error {
	if s.Kind == KindList {
		return fmt.Errorf("%s: %w", s.Env, err)
	}
	return fmt.Errorf("%s %w", s.Env, err)
}

// Layer is a set of values by key. The environment layer holds only the variables that were set.
type Layer map[string]Value

func (l Layer) value(key string) Value {
	if v, ok := l[key]; ok {
		return v
	}
	s, ok := Lookup(key)
	if !ok {
		panic("settings: unknown key " + key)
	}
	return s.Default
}

// Duration is the layer's value of a key, or the default.
func (l Layer) Duration(key string) time.Duration { return l.value(key).Dur }

// Int is the layer's value of a key, or the default.
func (l Layer) Int(key string) int { return l.value(key).Num }

// List is the layer's value of a key, or the default.
func (l Layer) List(key string) []string { return append([]string(nil), l.value(key).List...) }

// EnvLayer reads every variable of the registry. An empty variable counts as not set. A value that is refused stops the start.
func EnvLayer(get func(string) string) (Layer, error) {
	l := Layer{}
	for _, s := range specs {
		text := get(s.Env)
		if text == "" {
			continue
		}
		v, err := s.ParseText(text)
		if err != nil {
			return nil, s.envError(err)
		}
		l[s.Key] = v
	}
	return l, nil
}

// EnvValue reads the variable of one key. The runner uses it for REMEDY_RUN_TIMEOUT, the only one it knows.
func EnvValue(key string, get func(string) string) (Value, bool, error) {
	s, ok := Lookup(key)
	if !ok {
		panic("settings: unknown key " + key)
	}
	text := get(s.Env)
	if text == "" {
		return Value{}, false, nil
	}
	v, err := s.ParseText(text)
	if err != nil {
		return Value{}, false, s.envError(err)
	}
	return v, true, nil
}
