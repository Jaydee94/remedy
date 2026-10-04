package config

import (
	"errors"
	"log/slog"
	"strings"
)

// LogLevelFromEnv reads REMEDY_LOG_LEVEL: debug, info, warn or error, in any case. The default is info.
// At debug level the server logs every request to GitHub.
func LogLevelFromEnv(get func(string) string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(get("REMEDY_LOG_LEVEL"))) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, errors.New("REMEDY_LOG_LEVEL must be debug, info, warn or error")
}
