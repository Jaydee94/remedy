package main

import (
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

// The login relay has no switch (decision 2026-10-22): the runner runs it in subscription mode, the default, and never in
// another mode. A leftover REMEDY_LOGIN_RELAY in the environment changes nothing either way.
func TestTheLoginRelayRunsInSubscriptionModeWithoutASwitch(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"nothing set", nil, true},
		{"subscription", map[string]string{"REMEDY_AUTH_MODE": "subscription"}, true},
		{"an old switch set off", map[string]string{"REMEDY_LOGIN_RELAY": "off"}, true},
		{"api_key", map[string]string{"REMEDY_AUTH_MODE": "api_key"}, false},
		{"api_key with an old switch set on", map[string]string{"REMEDY_AUTH_MODE": "api_key", "REMEDY_LOGIN_RELAY": "on"}, false},
		{"another word", map[string]string{"REMEDY_AUTH_MODE": "Subscription"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{"REMEDY_RUNNER_TOKEN": "0123456789abcdef0123456789abcdef"}
			for k, v := range c.env {
				env[k] = v
			}
			cfg, err := config.RunnerFromEnv(func(k string) string { return env[k] })
			if err != nil {
				t.Fatal(err)
			}
			if got := loginRelayOn(cfg.AuthMode); got != c.want {
				t.Errorf("AuthMode %q: %v, want %v", cfg.AuthMode, got, c.want)
			}
		})
	}
}

func TestTheLoginRelayNeedsExactlySubscription(t *testing.T) {
	for mode, want := range map[string]bool{"subscription": true, "": false, "api_key": false, " subscription": false} {
		if got := loginRelayOn(mode); got != want {
			t.Errorf("loginRelayOn(%q) = %v, want %v", mode, got, want)
		}
	}
}
