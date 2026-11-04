package provider

import "testing"

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
