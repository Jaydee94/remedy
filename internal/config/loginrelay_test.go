package config_test

import (
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func TestLoginRelayOnlyForExactlyOn(t *testing.T) {
	cases := map[string]bool{
		"on": true, "ON": false, "On": false, "true": false, "1": false, " on": false, "on ": false, "off": false, "": false,
	}
	for v, want := range cases {
		if got := config.LoginRelayOn(v); got != want {
			t.Errorf("LoginRelayOn(%q) = %v, want %v", v, got, want)
		}
		s, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_LOGIN_RELAY": v}))
		if err != nil || s.LoginRelay != want {
			t.Errorf("server %q: LoginRelay = %v, err = %v, want %v", v, s.LoginRelay, err, want)
		}
		r, err := config.RunnerFromEnv(runnerEnv(map[string]string{"REMEDY_LOGIN_RELAY": v}))
		if err != nil || r.LoginRelay != want {
			t.Errorf("runner %q: LoginRelay = %v, err = %v, want %v", v, r.LoginRelay, err, want)
		}
	}
}

func TestLoginRelayIsOffWhenUnset(t *testing.T) {
	s, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil || s.LoginRelay {
		t.Fatalf("server: LoginRelay = %v, err = %v", s.LoginRelay, err)
	}
	r, err := config.RunnerFromEnv(runnerEnv(nil))
	if err != nil || r.LoginRelay {
		t.Fatalf("runner: LoginRelay = %v, err = %v", r.LoginRelay, err)
	}
}

func TestAuthModeDefaults(t *testing.T) {
	r, err := config.RunnerFromEnv(runnerEnv(nil))
	if err != nil || r.AuthMode != "subscription" {
		t.Fatalf("AuthMode = %q, err = %v, want subscription", r.AuthMode, err)
	}
	r, err = config.RunnerFromEnv(runnerEnv(map[string]string{"REMEDY_AUTH_MODE": "api_key"}))
	if err != nil || r.AuthMode != "api_key" {
		t.Fatalf("AuthMode = %q, err = %v, want api_key", r.AuthMode, err)
	}
}
