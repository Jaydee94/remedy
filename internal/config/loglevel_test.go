package config_test

import (
	"log/slog"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func TestLogLevelFromEnv(t *testing.T) {
	for value, want := range map[string]slog.Level{
		"":        slog.LevelInfo,
		"info":    slog.LevelInfo,
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		" warn ":  slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
	} {
		got, err := config.LogLevelFromEnv(env(map[string]string{"REMEDY_LOG_LEVEL": value}))
		if err != nil || got != want {
			t.Errorf("REMEDY_LOG_LEVEL=%q gives %v, %v, want %v", value, got, err, want)
		}
	}
	for _, bad := range []string{"verbose", "trace", "1"} {
		if _, err := config.LogLevelFromEnv(env(map[string]string{"REMEDY_LOG_LEVEL": bad})); err == nil {
			t.Errorf("REMEDY_LOG_LEVEL=%q was accepted", bad)
		}
	}
}
