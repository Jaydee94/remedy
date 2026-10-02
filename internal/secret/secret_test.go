package secret_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/secret"
)

func testKey(t *testing.T, fill byte) secret.Key {
	t.Helper()
	k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestParseKey(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if _, err := secret.ParseKey(valid); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if _, err := secret.ParseKey("  " + valid + "\n"); err != nil {
		t.Fatalf("surrounding whitespace should be ignored: %v", err)
	}

	bad := map[string]string{
		"empty":      "",
		"not base64": "!!!not-base64!!!",
		"too short":  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16)),
		"too long":   base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 33)),
	}
	for name, s := range bad {
		if _, err := secret.ParseKey(s); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	key := testKey(t, 7)
	sealed, err := key.Seal([]byte("ghp_topsecret"), "github_connection:1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := key.Open(sealed, "github_connection:1")
	if err != nil || string(got) != "ghp_topsecret" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestSealUsesAFreshNonceEveryTime(t *testing.T) {
	key := testKey(t, 7)
	a, _ := key.Seal([]byte("same"), "ctx")
	b, _ := key.Seal([]byte("same"), "ctx")
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext are identical, the nonce is reused")
	}
}

func TestSealedValueDoesNotContainThePlaintext(t *testing.T) {
	sealed, _ := testKey(t, 7).Seal([]byte("ghp_topsecret"), "ctx")
	if bytes.Contains(sealed, []byte("topsecret")) {
		t.Fatal("the plaintext is visible in the sealed value")
	}
}

func TestOpenRejectsWrongKeyWrongContextAndTampering(t *testing.T) {
	key := testKey(t, 7)
	sealed, _ := key.Seal([]byte("ghp_topsecret"), "ctx")

	if _, err := testKey(t, 8).Open(sealed, "ctx"); !errors.Is(err, secret.ErrOpen) {
		t.Errorf("wrong key: error = %v, want ErrOpen", err)
	}
	if _, err := key.Open(sealed, "another-row"); !errors.Is(err, secret.ErrOpen) {
		t.Errorf("wrong context: error = %v, want ErrOpen", err)
	}
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := key.Open(tampered, "ctx"); !errors.Is(err, secret.ErrOpen) {
		t.Errorf("tampered: error = %v, want ErrOpen", err)
	}
	if _, err := key.Open(sealed[:5], "ctx"); !errors.Is(err, secret.ErrOpen) {
		t.Errorf("truncated: error = %v, want ErrOpen", err)
	}
}

func TestValueNeverRevealsItsContent(t *testing.T) {
	v := secret.NewValue("ghp_topsecret")

	var text bytes.Buffer
	slog.New(slog.NewTextHandler(&text, nil)).Info("x", "token", v)
	var js bytes.Buffer
	slog.New(slog.NewJSONHandler(&js, nil)).Info("x", "token", v)
	marshaled, err := json.Marshal(map[string]any{"token": v})
	if err != nil {
		t.Fatal(err)
	}

	outputs := []string{
		fmt.Sprint(v),
		fmt.Sprintf("%v|%+v|%#v|%s", v, v, v, v),
		string(marshaled),
		text.String(),
		js.String(),
	}
	for _, o := range outputs {
		if strings.Contains(o, "topsecret") {
			t.Errorf("secret leaked in %q", o)
		}
	}
	if !strings.Contains(string(marshaled), "***") {
		t.Errorf("JSON form = %s, want ***", marshaled)
	}
	if v.Reveal() != "ghp_topsecret" {
		t.Errorf("Reveal() = %q", v.Reveal())
	}
}

func TestKeyNeverPrintsItsBytes(t *testing.T) {
	k := testKey(t, 7)
	out := fmt.Sprintf("%v|%+v|%#v|%s", k, k, k, k)
	if strings.Contains(out, "\x07") || strings.Contains(out, "[7 7 7") {
		t.Fatalf("key bytes leaked in %q", out)
	}
	if !strings.Contains(out, "***") {
		t.Fatalf("output = %q, want ***", out)
	}
}
