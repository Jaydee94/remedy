package loginrelay

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const planted = "planted-9f3k"

type holder struct {
	Code Material `json:"code"`
}

func noLeak(t *testing.T, what, got string) {
	t.Helper()
	if strings.Contains(got, planted) || strings.Contains(got, hex.EncodeToString([]byte(planted))) {
		t.Fatalf("%s leaks the material: %q", what, got)
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("%s has no stars: %q", what, got)
	}
}

func TestMaterialPrintsStarsEverywhere(t *testing.T) {
	m := NewMaterial(planted)
	h := holder{Code: m}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%08.3v"} {
		noLeak(t, "Sprintf "+verb, fmt.Sprintf(verb, m))
		noLeak(t, "Sprintf struct "+verb, fmt.Sprintf(verb, h))
		noLeak(t, "Sprintf pointer "+verb, fmt.Sprintf(verb, &h))
	}
	noLeak(t, "String", m.String())
	noLeak(t, "GoString", m.GoString())
	noLeak(t, "Sprint", fmt.Sprint(m))
	noLeak(t, "Sprintln", fmt.Sprintln(m))

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	noLeak(t, "json value", string(b))
	if string(b) != `"***"` {
		t.Fatalf("json = %s", b)
	}
	b, err = json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	noLeak(t, "json struct", string(b))
	b, err = json.Marshal(map[string]Material{"code": m})
	if err != nil {
		t.Fatal(err)
	}
	noLeak(t, "json map", string(b))
	b, err = json.Marshal(&h)
	if err != nil {
		t.Fatal(err)
	}
	noLeak(t, "json pointer", string(b))

	for name, mk := range map[string]func(*bytes.Buffer) *slog.Logger{
		"text": func(w *bytes.Buffer) *slog.Logger { return slog.New(slog.NewTextHandler(w, nil)) },
		"json": func(w *bytes.Buffer) *slog.Logger { return slog.New(slog.NewJSONHandler(w, nil)) },
	} {
		var buf bytes.Buffer
		mk(&buf).Info("x", slog.Any("code", m))
		noLeak(t, "slog "+name, buf.String())
		buf.Reset()
		mk(&buf).Info("x", slog.Group("login", slog.Any("code", m)))
		noLeak(t, "slog group "+name, buf.String())
		buf.Reset()
		mk(&buf).Info("x", "code", m)
		noLeak(t, "slog pair "+name, buf.String())
	}

	if string(m.Bytes()) != planted || m.Len() != len(planted) {
		t.Fatal("the material is not readable through Bytes")
	}
}

func TestMaterialZeroClearsEveryCopy(t *testing.T) {
	m := NewMaterial(planted)
	c := m
	c.Zero()
	for _, b := range m.Bytes() {
		if b != 0 {
			t.Fatalf("the original still holds %q", m.Bytes())
		}
	}
	if m.Len() != len(planted) {
		t.Fatal("length changed")
	}
}

func TestMaterialCopiesItsInput(t *testing.T) {
	var zero Material
	if zero.Len() != 0 || len(zero.Bytes()) != 0 {
		t.Fatal("zero value is not empty")
	}
	zero.Zero()
	noLeak(t, "zero value", zero.String())
}
