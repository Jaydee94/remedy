package main

import (
	"strings"
	"testing"
)

func getenv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestKeyModeNeedsAKey(t *testing.T) {
	for _, env := range []map[string]string{nil, {"ANTHROPIC_API_KEY": ""}, {"ANTHROPIC_API_KEY": "  "}} {
		_, err := checkAuth("api_key", getenv(env))
		if err == nil || err.Error() != "REMEDY_AUTH_MODE=api_key needs ANTHROPIC_API_KEY in the runner's environment" {
			t.Errorf("env %v: err = %v, want the fixed message", env, err)
		}
	}
	if warn, err := checkAuth("api_key", getenv(map[string]string{"ANTHROPIC_API_KEY": "sk-ant-secret-value"})); err != nil || warn != "" {
		t.Errorf("a key in key mode: warn %q, err %v, want neither", warn, err)
	}
}

func TestSubscriptionModeIgnoresAKeyAndSaysSoWithoutIt(t *testing.T) {
	const key = "sk-ant-secret-value"
	warn, err := checkAuth("subscription", getenv(map[string]string{"ANTHROPIC_API_KEY": key}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn, "ANTHROPIC_API_KEY is set but ignored") || strings.Contains(warn, key) || strings.Contains(warn, "secret") {
		t.Errorf("warning = %q, want a sentence that does not contain the key", warn)
	}
	if warn, err := checkAuth("subscription", getenv(nil)); err != nil || warn != "" {
		t.Errorf("no key in subscription mode: warn %q, err %v, want neither", warn, err)
	}
}
