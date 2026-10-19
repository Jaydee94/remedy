package prompt_test

import (
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/prompt"
)

// A key longer than the key limit must not be cut before the redaction: the rule for `..._password=value` needs the end of the key.
func TestASecretBehindALongKeyIsRedacted(t *testing.T) {
	long := strings.Repeat("a", 70) + "_password"
	in := input()
	in.Labels = map[string]string{"alertname": "A", long: "hunter2hunter2"}
	in.Annotations = map[string]string{long: "hunter2hunter2"}
	text := prompt.Outage(in, d)
	if strings.Contains(text, "hunter2hunter2") {
		t.Errorf("the value reached the prompt:\n%s", text)
	}
}
