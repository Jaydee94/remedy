// Package redact removes secrets from text before it is handed to an agent CLI. It is a safety net,
// not a guarantee: it prefers to remove too much, because a diagnosis never needs the value of a secret.
package redact

import "regexp"

type rule struct {
	re   *regexp.Regexp
	with string
}

// keyName is an identifier that contains a word that usually names a secret, such as
// AWS_SECRET_ACCESS_KEY, db_password or api-key.
const keyName = `[A-Za-z0-9_.-]*(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)[A-Za-z0-9_.-]*["']?`

// The order matters for the first two rules: the complete key block must go before the pattern for a cut-off
// block, which would swallow everything after it.
var rules = []rule{
	{regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), "[REDACTED:private-key]"},
	// A key block that was cut off has no END line: everything up to the end of the text goes.
	{regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*`), "[REDACTED:private-key]"},
	{regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`), "[REDACTED:github-token]"},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), "[REDACTED:github-token]"},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), "[REDACTED:aws-key]"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), "[REDACTED:jwt]"},
	{regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]{8,}`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)(https?://)[^\s/:@]+:[^\s/@]+@`), "${1}[REDACTED]@"},
	{regexp.MustCompile(`(?i)(` + keyName + `\s*[:=]\s*)("[^"\n]*"|'[^'\n]*'|[^\s"',;]+)`), "${1}[REDACTED]"},
}

// Redact replaces what looks like a secret in s with a marker. It is idempotent.
func Redact(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}
