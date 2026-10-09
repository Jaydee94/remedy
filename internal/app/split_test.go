package app_test

import (
	"bytes"
	"encoding/base64"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/store"
)

func appFor(t *testing.T, extra map[string]string) *app.App {
	t.Helper()
	env := map[string]string{
		"REMEDY_ADMIN_PASSWORD": password,
		"REMEDY_RUNNER_TOKEN":   runnerToken,
		"REMEDY_MASTER_KEY":     secret32(),
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return app.New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}

func TestWithoutAnInternalAddrThereIsOneHandler(t *testing.T) {
	if a := appFor(t, nil); a.InternalHandler != nil {
		t.Fatal("InternalHandler must be nil when REMEDY_INTERNAL_ADDR is not set")
	}
}

func TestWithAnInternalAddrThereAreTwoHandlers(t *testing.T) {
	if a := appFor(t, map[string]string{"REMEDY_INTERNAL_ADDR": ":8081"}); a.InternalHandler == nil {
		t.Fatal("InternalHandler must be set when REMEDY_INTERNAL_ADDR is set")
	}
}

// secret32 is a valid REMEDY_MASTER_KEY: 32 bytes in Base64.
func secret32() string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)) }
