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
	if warn, err := checkAuth("api_key", getenv(map[string]string{"ANTHROPIC_API_KEY": "sk-ant-secret-value-0123456789"})); err != nil || warn != "" {
		t.Errorf("a key in key mode: warn %q, err %v, want neither", warn, err)
	}
}

func TestSubscriptionModeIgnoresAKeyAndSaysSoWithoutIt(t *testing.T) {
	const key = "sk-ant-secret-value-0123456789"
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

// The scrubber finds a key by its characters, and a key with surrounding whitespace or an odd character would make every
// run fail without saying why: it is refused at start-up, with a sentence that does not hold the value.
func TestKeyModeRefusesAKeyThatCannotBeScrubbedOrUsed(t *testing.T) {
	for name, key := range map[string]string{
		"a newline at the end": "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789\n",
		"a space inside":       "sk-ant-api03-abcdefghij klmnopqrstuvwxyz",
		"a quote":              "sk-ant-api03-abcdefghij\"klmnopqrstuvwxyz",
		"a backslash":          "sk-ant-api03-abcdefghij\\klmnopqrstuvwxyz",
		"too short":            "sk-ant-x",
		"a single letter":      "n",
	} {
		_, err := checkAuth("api_key", getenv(map[string]string{"ANTHROPIC_API_KEY": key}))
		if err == nil {
			t.Errorf("%s: the key was accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "abcdefghij") || strings.Contains(err.Error(), "sk-ant") {
			t.Errorf("%s: the error holds the key: %v", name, err)
		}
	}
}
