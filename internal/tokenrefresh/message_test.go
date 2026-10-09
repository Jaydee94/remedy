package tokenrefresh

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAPIMessageIsShortenedOnACharacterBoundary(t *testing.T) {
	// Every 3-byte character: the limit falls inside one for every offset that is not a multiple of 3.
	raw, _ := json.Marshal(map[string]string{"message": strings.Repeat("€", 400)})
	got := (&client{}).apiMessage(raw)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "...") || len(got) > maxMessage+3 {
		t.Fatalf("message = %q (%d bytes)", got, len(got))
	}
}
