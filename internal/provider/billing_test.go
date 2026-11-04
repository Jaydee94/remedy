package provider

import (
	"context"
	"testing"
)

func TestParseLineReadsTheKeySourceOfAnInitEvent(t *testing.T) {
	for name, c := range map[string]struct {
		line   string
		init   bool
		source string
		known  bool
	}{
		"a subscription": {`{"type":"system","subtype":"init","apiKeySource":"none","session_id":"s"}`, true, "none", true},
		"a key":          {`{"type":"system","subtype":"init","apiKeySource":"ANTHROPIC_API_KEY"}`, true, "ANTHROPIC_API_KEY", true},
		"no field":       {`{"type":"system","subtype":"init","session_id":"s"}`, true, "", false},
		"another system": {`{"type":"system","subtype":"compact","apiKeySource":"none"}`, false, "", false},
		"an assistant":   {`{"type":"assistant","apiKeySource":"x"}`, false, "", false},
		"not JSON":       {`not json`, false, "", false},
	} {
		l := Claude{}.ParseLine([]byte(c.line))
		if (l.Init != nil) != c.init {
			t.Errorf("%s: Init = %v, want set = %v", name, l.Init, c.init)
			continue
		}
		if l.Init != nil && (l.Init.APIKeySource != c.source || l.Init.Known != c.known) {
			t.Errorf("%s: Init = %+v, want source %q known %v", name, *l.Init, c.source, c.known)
		}
	}
}

func TestEachModeAcceptsOnlyItsOwnBilling(t *testing.T) {
	for name, c := range map[string]struct {
		mode   Mode
		source string
		known  bool
		want   bool
	}{
		"subscription, none":     {ModeSubscription, "none", true, true},
		"subscription, a key":    {ModeSubscription, "ANTHROPIC_API_KEY", true, false},
		"subscription, no field": {ModeSubscription, "", false, true},
		"the zero mode, a key":   {"", "ANTHROPIC_API_KEY", true, false},
		"key, a key":             {ModeAPIKey, "ANTHROPIC_API_KEY", true, true},
		"key, none":              {ModeAPIKey, "none", true, false},
		"key, no field":          {ModeAPIKey, "", false, false},
		"key, an empty value":    {ModeAPIKey, "", true, false},
		"key, another source":    {ModeAPIKey, "apiKeyHelper", true, true},
	} {
		if got := c.mode.Accepts(c.source, c.known); got != c.want {
			t.Errorf("%s: Accepts = %v, want %v", name, got, c.want)
		}
	}
}

func TestAuthSourceOfNamesTheBilling(t *testing.T) {
	for _, c := range []struct {
		source string
		known  bool
		want   string
	}{{"none", true, "subscription"}, {"ANTHROPIC_API_KEY", true, "api_key"}, {"", false, ""}, {"", true, ""}} {
		if got := AuthSourceOf(c.source, c.known); got != c.want {
			t.Errorf("AuthSourceOf(%q, %v) = %q, want %q", c.source, c.known, got, c.want)
		}
	}
}

func argsOf(t *testing.T, c Claude, spec Spec) []string {
	t.Helper()
	cmd := c.Command(context.Background(), spec, nil)
	return cmd.Args
}

func valueAfter(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// --max-budget-usd belongs to the key mode only; the money is whole cents with two decimals.
func TestTheBudgetFlagIsPassedInKeyModeOnly(t *testing.T) {
	for cents, want := range map[int]string{100: "1.00", 5: "0.05", 2000: "20.00", 1234: "12.34"} {
		got, ok := valueAfter(argsOf(t, Claude{Mode: ModeAPIKey}, Spec{Prompt: "p", MaxBudgetCents: cents}), "--max-budget-usd")
		if !ok || got != want {
			t.Errorf("%d cents: --max-budget-usd = %q (present %v), want %q", cents, got, ok, want)
		}
	}
	// A key-mode runner with no value in the claim falls back to 1.00: cost fails closed.
	if got, ok := valueAfter(argsOf(t, Claude{Mode: ModeAPIKey}, Spec{Prompt: "p"}), "--max-budget-usd"); !ok || got != "1.00" {
		t.Errorf("no value: --max-budget-usd = %q (present %v), want the built-in 1.00", got, ok)
	}
	for _, m := range []Mode{ModeSubscription, ""} {
		if _, ok := valueAfter(argsOf(t, Claude{Mode: m}, Spec{Prompt: "p", MaxBudgetCents: 100}), "--max-budget-usd"); ok {
			t.Errorf("mode %q got --max-budget-usd", m)
		}
	}
}
