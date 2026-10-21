package loginrelay

import (
	"net/url"
	"strings"
)

const (
	MaxURLBytes  = 2048
	MinCodeBytes = 8
	MaxCodeBytes = 512
)

const (
	urlPrefix = "https://claude.com/cai/oauth/authorize"
	urlPath   = "/cai/oauth/authorize"
	urlHost   = "claude.com"
)

// queryNames is the set of parameters the CLI was measured to send. A version
// that adds one fails closed.
var queryNames = map[string]bool{
	"code": true, "client_id": true, "response_type": true, "redirect_uri": true,
	"scope": true, "code_challenge": true, "code_challenge_method": true, "state": true,
}

// printable reports whether every byte is 0x21 to 0x7E: no space, no control
// character, no DEL, nothing outside ASCII.
func printable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7E {
			return false
		}
	}
	return true
}

// ValidateURL accepts only the sign-in address of the CLI: https, the host
// claude.com exactly, the path /cai/oauth/authorize exactly, no fragment, and
// only the measured query names, none twice, with code_challenge and state
// present. Values are not judged. The error never holds the input.
func ValidateURL(raw string) error {
	if raw == "" || len(raw) > MaxURLBytes || !printable(raw) || strings.ContainsRune(raw, '\\') {
		return ErrBadURL
	}
	// url.Parse lowers the scheme, so the raw text has to carry it exactly.
	// The prefix also pins the host in the raw text (case, port, userinfo, a
	// trailing dot) and the path before the query.
	rest, ok := strings.CutPrefix(raw, urlPrefix)
	if !ok || !strings.HasPrefix(rest, "?") {
		return ErrBadURL
	}
	if strings.ContainsRune(raw, '#') {
		return ErrBadURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ErrBadURL
	}
	if u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Host != urlHost ||
		u.Path != urlPath || u.RawPath != "" || u.Fragment != "" || u.RawFragment != "" {
		return ErrBadURL
	}
	seen := make(map[string]bool, len(queryNames))
	for _, part := range strings.Split(u.RawQuery, "&") {
		name, _, _ := strings.Cut(part, "=")
		if !queryNames[name] || seen[name] {
			return ErrBadURL
		}
		seen[name] = true
	}
	if !seen["code_challenge"] || !seen["state"] {
		return ErrBadURL
	}
	return nil
}

// ValidateCode trims surrounding white space and accepts 8 to 512 bytes of
// 0x21 to 0x7E, so the code is one line and nothing can be smuggled onto a
// second prompt. It returns the trimmed code. The error never holds the input.
func ValidateCode(raw string) (string, error) {
	code := strings.Trim(raw, " \t\r\n\v\f")
	if len(code) < MinCodeBytes || len(code) > MaxCodeBytes || !printable(code) {
		return "", ErrBadCode
	}
	return code, nil
}
