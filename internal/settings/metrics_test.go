package settings_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/settings"
)

func TestTheMetricsAddressIsCheckedLikeTheAlertmanagerAddress(t *testing.T) {
	s := spec(t, settings.MetricsURL)
	if s.Key != "metrics_url" || s.Group != "Metrics" || s.Label != "Metrics address" || s.Env != "REMEDY_METRICS_URL" || s.Kind != settings.KindText {
		t.Errorf("spec = %+v", s)
	}
	good := map[string]string{
		"":                                       "",
		"http://victoria:8428":                   "http://victoria:8428",
		"https://vm.example/select/0/prometheus": "https://vm.example/select/0/prometheus",
		"https://vm.example/select/0/prometheus/": "https://vm.example/select/0/prometheus",
		"  http://x  ": "http://x",
	}
	for in, want := range good {
		v, err := s.ParseText(in)
		if err != nil || v.Str != want {
			t.Errorf("%q: %q, %v, want %q", in, v.Str, err, want)
		}
	}
	for _, in := range []string{
		"ftp://x", "victoria:8428", "http://", "http://user@host", "http://user:pw@host", "http://h?x=1", "http://h?", "http://h#frag",
		"http://h space", "http://h\nx", "javascript:alert(1)", "http://" + strings.Repeat("a", 2100),
	} {
		if _, err := s.ParseText(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}

func TestTheMetricsAddressComesFromTheEnvironment(t *testing.T) {
	l, err := settings.EnvLayer(env(map[string]string{"REMEDY_METRICS_URL": "http://vm:8428/"}))
	if err != nil || l.Text(settings.MetricsURL) != "http://vm:8428" {
		t.Fatalf("%v, %v", l, err)
	}
	_, err = settings.EnvLayer(env(map[string]string{"REMEDY_METRICS_URL": "http://u:p@vm"}))
	if err == nil || !strings.HasPrefix(err.Error(), "REMEDY_METRICS_URL must be an http or https address") {
		t.Fatalf("error = %v", err)
	}
}

func TestTheMetricsTokenIsASecretThatIsWriteOnly(t *testing.T) {
	sp := spec(t, settings.MetricsToken)
	if sp.Key != "metrics_token" || sp.Group != "Metrics" || sp.Kind != settings.KindSecret || sp.Env != "" {
		t.Errorf("spec = %+v", sp)
	}
	s, _, _ := newKeyed(t, key(t, 5), io.Discard)
	ctx := context.Background()
	e, err := s.Set(ctx, settings.MetricsToken, json.RawMessage(`"`+plainToken+`"`))
	if err != nil || e.Source != settings.SourceSet || e.Hint != "…CRET" {
		t.Fatalf("entry = %+v, %v", e, err)
	}
	got, err := s.Secret(settings.MetricsToken)
	if err != nil || got.Reveal() != plainToken || got.String() != "***" {
		t.Fatalf("Secret = %q, %v", got.Reveal(), err)
	}
	for _, en := range s.Entries() {
		b, _ := json.Marshal(en.Value.JSON(en.Spec.Kind))
		if strings.Contains(string(b)+en.Hint+en.Problem, "0123456789abcdef") {
			t.Fatalf("the token is shown in the entry of %s", en.Spec.Key)
		}
	}
	for _, raw := range []string{`"two\nlines"`, `""`, `5`, `null`} {
		if _, err := s.Set(ctx, settings.MetricsToken, json.RawMessage(raw)); err == nil {
			t.Errorf("%s was accepted", raw)
		}
	}
}
