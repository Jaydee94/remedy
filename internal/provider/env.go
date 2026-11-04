package provider

import "strings"

// Mode says how the agent CLI is billed: with the subscription login of the operator, or with an API key in the runner's
// environment. It is chosen once per installation (the runner's REMEDY_AUTH_MODE), never from the UI. The zero Mode is
// the subscription: every caller that predates the key mode keeps its behaviour.
type Mode string

const (
	ModeSubscription Mode = "subscription"
	ModeAPIKey       Mode = "api_key"
)

// Valid says whether m is one of the two modes the runner accepts.
func (m Mode) Valid() bool { return m == ModeSubscription || m == ModeAPIKey }

// modeVar is the one variable that belongs to a mode and to no other: the OAuth token of a `setup-token` login in the
// subscription mode, the key in the key mode.
func modeVar(m Mode) string {
	if m == ModeAPIKey {
		return "ANTHROPIC_API_KEY"
	}
	return "CLAUDE_CODE_OAUTH_TOKEN"
}

// EnvFor builds the environment of a CLI subprocess from the runner's own: the allowlist that both modes share plus the
// variable of the mode, in the order of parent. Nothing else passes. ANTHROPIC_AUTH_TOKEN and ANTHROPIC_BASE_URL never do (a
// base URL would send the key and the prompts elsewhere), and neither do the variables of Bedrock, Vertex and Foundry. A
// mode that is not one of the two is treated as the subscription.
func EnvFor(mode Mode, parent []string) []string {
	own := modeVar(mode)
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		key, _, ok := strings.Cut(kv, "=")
		if ok && (envAllowlist[key] || key == own) {
			out = append(out, kv)
		}
	}
	return out
}
