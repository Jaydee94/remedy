// Package settings holds the values the maintainer changes in the Setup page. A registry in the code says which keys exist and what
// each may hold; the environment is the default, a row in the table `settings` overrides it, and a Settings reader answers every
// component from an in-memory snapshot.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	AlertmanagerURL        = "alertmanager_url"
	AlertmanagerToken      = "alertmanager_token"
	AlertmanagerAutoMaxAge = "alertmanager_auto_max_age"
	AutoApprovalTimeout    = "auto_approval_timeout"
	MetricsURL             = "metrics_url"
	MetricsToken           = "metrics_token"
)

// Spec says what one setting is. problem is the sentence that follows the variable's name when a value is refused; check is the
// bounds of the value.
type Spec struct {
	Key     string
	Group   string // the heading on the Setup page
	Label   string
	Env     string // the environment variable that gives the default; empty: the setting has none
	Kind    Kind
	Default Value
	Rule    string // the bounds, for the Setup page
	Effect  string // when a change takes effect, for the Setup page
	problem string
	check   func(Value) bool
	// normalize writes a value that passed check in its one canonical form (a trailing slash removed); nil: as it is.
	normalize func(Value) Value
}

var specs = []Spec{
	{
		Key: PollInterval, Group: "Polling", Label: "How often I check GitHub", Env: "REMEDY_POLL_INTERVAL",
		Kind: KindDuration, Default: Value{Dur: time.Minute}, Rule: "At least 10s, for example 60s or 2m. It is also how often I ask Alertmanager.",
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
	{
		Key: AlertmanagerURL, Group: "Alertmanager", Label: "Alertmanager address", Env: "REMEDY_ALERTMANAGER_URL",
		Kind: KindText, Rule: "An http or https address without a user name, for example http://alertmanager:9093. Empty: I do not ask Alertmanager.",
		Effect:    "Takes effect at the next check.",
		problem:   "must be an http or https address without a user name, a query or a fragment, for example http://alertmanager:9093",
		check:     func(v Value) bool { return validAlertmanagerURL(v.Str) },
		normalize: func(v Value) Value { v.Str = strings.TrimRight(v.Str, "/"); return v },
	},
	{
		Key: AlertmanagerToken, Group: "Alertmanager", Label: "Alertmanager token",
		Kind: KindSecret, Rule: "Written once and never shown again. Empty: I send no token.",
		Effect:  "Takes effect at the next request.",
		problem: "must be 1 to 4096 characters without a line break or a control character",
		check:   func(v Value) bool { return validSecretText(v.Str) },
	},
	{
		Key: AlertmanagerAutoMaxAge, Group: "Alertmanager", Label: "Oldest alert I diagnose on my own",
		Kind: KindDuration, Default: Value{Dur: time.Hour}, Rule: "At least 1m, for example 1h. An older alert is shown, and a click diagnoses it.",
		Effect:  "Takes effect when an incident opens.",
		problem: "must be a duration of at least 1m, for example 1h",
		check:   func(v Value) bool { return v.Dur >= time.Minute },
	},
	{
		Key: MetricsURL, Group: "Metrics", Label: "Metrics address", Env: "REMEDY_METRICS_URL",
		Kind: KindText, Rule: "An http or https address without a user name, for example http://victoria:8428 or https://host/select/0/prometheus. Empty: I do not ask for metrics.",
		Effect:    "Takes effect at the next request.",
		problem:   "must be an http or https address without a user name, a query or a fragment, for example http://victoria:8428",
		check:     func(v Value) bool { return validAlertmanagerURL(v.Str) }, // the same rules as Alertmanager's address
		normalize: func(v Value) Value { v.Str = strings.TrimRight(v.Str, "/"); return v },
	},
	{
		Key: MetricsToken, Group: "Metrics", Label: "Metrics token",
		Kind: KindSecret, Rule: "Written once and never shown again. Empty: I send no token.",
		Effect:  "Takes effect at the next request.",
		problem: "must be 1 to 4096 characters without a line break or a control character",
		check:   func(v Value) bool { return validSecretText(v.Str) },
	},
	{
		Key: AutoApprovalTimeout, Group: "Runs", Label: "How long I wait for an approval in an automatic run", Env: "REMEDY_AUTO_APPROVAL_TIMEOUT",
		Kind: KindDuration, Default: Value{Dur: 15 * time.Minute}, Rule: "0 means I wait without a limit; otherwise at least 1m, for example 15m. A run you start yourself always waits.",
		Effect:  "Takes effect when an approval is asked for; a question that already waits keeps its limit.",
		problem: "must be 0 or a duration of at least 1m, for example 15m",
		check:   func(v Value) bool { return v.Dur == 0 || v.Dur >= time.Minute },
	},
}

// validAlertmanagerURL says whether s is empty or an http or https address that carries no credential, query or fragment.
func validAlertmanagerURL(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 2048 || strings.ContainsAny(s, " \t\r\n\x00#?") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	return u.User == nil && u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery
}

// validSecretText says whether s can be a secret: 1 to 4096 bytes without a control character (a line break included). The
// sentence that refuses it never contains s.
func validSecretText(s string) bool {
	if len(s) < 1 || len(s) > 4096 {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
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
	case KindText, KindSecret:
		v.Str = strings.TrimSpace(text)
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
	if s.normalize != nil {
		v = s.normalize(v)
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
	case KindText, KindSecret:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil || isNull {
			return Value{}, fmt.Errorf("%s (write it as a string)", s.problem)
		}
		return s.ParseText(text)
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

// Text is the layer's value of a key, or the default.
func (l Layer) Text(key string) string { return l.value(key).Str }

// EnvLayer reads every variable of the registry. An empty variable counts as not set, and a setting without a variable is skipped.
// A value that is refused stops the start.
func EnvLayer(get func(string) string) (Layer, error) {
	l := Layer{}
	for _, s := range specs {
		if s.Env == "" {
			continue
		}
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
	if s.Env == "" {
		return Value{}, false, nil
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
