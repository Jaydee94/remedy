package provider

import (
	"slices"
	"testing"
)

// parent is the environment of a runner pod that has everything wrong with it at once.
var parent = []string{
	"PATH=/usr/bin", "HOME=/state", "ANTHROPIC_API_KEY=sk-ant-x", "CLAUDE_CODE_OAUTH_TOKEN=tok",
	"ANTHROPIC_AUTH_TOKEN=a", "ANTHROPIC_BASE_URL=http://evil", "AWS_ACCESS_KEY_ID=k", "CLAUDE_CODE_USE_BEDROCK=1", "LANG=C",
}

func TestEnvForKeepsOnlyWhatTheModeAllows(t *testing.T) {
	for name, tc := range map[string]struct {
		mode Mode
		want []string
	}{
		"subscription":    {ModeSubscription, []string{"PATH=/usr/bin", "HOME=/state", "CLAUDE_CODE_OAUTH_TOKEN=tok", "LANG=C"}},
		"api_key":         {ModeAPIKey, []string{"PATH=/usr/bin", "HOME=/state", "ANTHROPIC_API_KEY=sk-ant-x", "LANG=C"}},
		"the zero mode":   {"", []string{"PATH=/usr/bin", "HOME=/state", "CLAUDE_CODE_OAUTH_TOKEN=tok", "LANG=C"}},
		"an unknown mode": {Mode("bedrock"), []string{"PATH=/usr/bin", "HOME=/state", "CLAUDE_CODE_OAUTH_TOKEN=tok", "LANG=C"}},
	} {
		if got := EnvFor(tc.mode, parent); !slices.Equal(got, tc.want) {
			t.Errorf("%s: EnvFor = %v, want %v", name, got, tc.want)
		}
	}
}

func TestNoModeLetsTheOtherAuthVariablesThrough(t *testing.T) {
	for _, m := range []Mode{ModeSubscription, ModeAPIKey, ""} {
		for _, kv := range EnvFor(m, parent) {
			for _, banned := range []string{"ANTHROPIC_AUTH_TOKEN=", "ANTHROPIC_BASE_URL=", "AWS_", "CLAUDE_CODE_USE_"} {
				if len(kv) >= len(banned) && kv[:len(banned)] == banned {
					t.Errorf("mode %q passed %s", m, kv)
				}
			}
		}
	}
}

func TestModeValid(t *testing.T) {
	for m, want := range map[Mode]bool{ModeSubscription: true, ModeAPIKey: true, "": false, "apikey": false, "API_KEY": false} {
		if m.Valid() != want {
			t.Errorf("Mode(%q).Valid() = %v, want %v", m, m.Valid(), want)
		}
	}
}
