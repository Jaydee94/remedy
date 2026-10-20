package prompt_test

import (
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/prompt"
)

// A zero width character inside a secret must not keep the secret from being redacted: the characters are removed first, then the text is redacted.
func TestAZeroWidthCharacterInsideASecretDoesNotHideItFromRedaction(t *testing.T) {
	secret := "ghp_" + strings.Repeat("a1B2c3D4e5", 3) + "a1B2c3"
	hidden := secret[:8] + string(rune(0x200B)) + secret[8:]
	in := prompt.OutageInput{
		Title:       "t",
		Severity:    "critical",
		Labels:      map[string]string{"note": hidden},
		Annotations: map[string]string{"description": "token " + hidden},
	}
	text := prompt.Outage(in, "0123456789abcdef0123456789abcdef")
	if strings.Contains(text, secret) || strings.Contains(text, secret[:8]) && strings.Contains(text, secret[8:20]) {
		t.Fatalf("a secret with a zero width character inside reached the prompt:\n%s", text)
	}
	if strings.ContainsRune(text, 0x200B) {
		t.Fatal("a zero width character reached the prompt")
	}
}
