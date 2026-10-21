package main

import (
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func TestTheLoginRelayRunsOnlyWhenOnAndInSubscriptionMode(t *testing.T) {
	cases := []struct {
		on   bool
		mode string
		want bool
	}{
		{true, "subscription", true},
		{false, "subscription", false},
		{true, "api_key", false},
		{true, "", false},
		{false, "api_key", false},
	}
	for _, c := range cases {
		if got := loginRelayOn(config.Runner{LoginRelay: c.on, AuthMode: c.mode}); got != c.want {
			t.Errorf("LoginRelay %v, AuthMode %q: %v, want %v", c.on, c.mode, got, c.want)
		}
	}
}
