// Package logging configures the process-wide slog logger: level-gated JSON
// (default) or text on stdout, with normalized output keys — "ts"
// (RFC3339Nano), lowercase "level", and a short "caller" at debug.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Env vars follow the project-wide RBLN_DEVICE_PLUGIN_* prefix so generic
// names cannot be captured by unrelated env injection.
const (
	envLogLevel  = "RBLN_DEVICE_PLUGIN_LOG_LEVEL"
	envLogFormat = "RBLN_DEVICE_PLUGIN_LOG_FORMAT"
)

func newLogger(w io.Writer, lvl slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: lvl,
		// The caller attr's cost and noise are only worth it at debug.
		AddSource:   lvl <= slog.LevelDebug,
		ReplaceAttr: replaceAttr,
	}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}

// Settings lets callers name the effective level and format in their startup
// record, so the stream states the gate it is being read through.
type Settings struct {
	Level  string
	Format string
}

// SetupFromEnv reads RBLN_DEVICE_PLUGIN_LOG_LEVEL / _LOG_FORMAT and installs
// the process-wide default logger (stdout), returning the settings it resolved.
// Empty values default to info/json (the production defaults). Invalid
// values do not kill the process: only the offending variable falls back
// to its default, and a Warn carrying a "fallback" key is emitted through
// the installed logger.
func SetupFromEnv() Settings {
	logger, settings := setupFromEnv(os.Stdout)
	slog.SetDefault(logger)
	return settings
}

// setupFromEnv is split from SetupFromEnv, and takes w rather than defaulting
// to stdout, so tests can observe the invalid-value warns.
func setupFromEnv(w io.Writer) (*slog.Logger, Settings) {
	lvl, levelErr := parseLevel(os.Getenv(envLogLevel))
	if levelErr != nil {
		lvl = slog.LevelInfo
	}
	format, formatErr := parseFormat(os.Getenv(envLogFormat))
	if formatErr != nil {
		format = "json"
	}
	logger := newLogger(w, lvl, format)
	// With level=error an invalid format's warn is suppressed by the gate —
	// accepted, since the error gate was chosen explicitly.
	if levelErr != nil {
		logger.Warn("Invalid "+envLogLevel+", using default", "err", levelErr, "fallback", "info")
	}
	if formatErr != nil {
		logger.Warn("Invalid "+envLogFormat+", using default", "err", formatErr, "fallback", "json")
	}
	return logger, Settings{Level: strings.ToLower(lvl.String()), Format: format}
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "error":
		return slog.LevelError, nil
	case "warning", "warn":
		return slog.LevelWarn, nil
	case "debug":
		return slog.LevelDebug, nil
	}
	return 0, fmt.Errorf("unknown log level %q (error|warning|warn|info|debug)", s)
}

func parseFormat(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "json":
		return "json", nil
	case "text":
		return "text", nil
	}
	return "", fmt.Errorf("unknown log format %q (json|text)", s)
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		// String-valued user "time" attrs pass through; a time-valued one is
		// indistinguishable from the record timestamp and gets rewritten too.
		if a.Value.Kind() != slog.KindTime {
			return a
		}
		a.Key = "ts"
		a.Value = slog.StringValue(a.Value.Time().Format(time.RFC3339Nano))
	case slog.LevelKey:
		lvl, ok := a.Value.Any().(slog.Level)
		if !ok {
			return a
		}
		a.Value = slog.StringValue(strings.ToLower(lvl.String()))
	case slog.SourceKey:
		src, ok := a.Value.Any().(*slog.Source)
		if !ok {
			return a
		}
		a.Key = "caller"
		a.Value = slog.StringValue(fmt.Sprintf("%s:%d", trimPath(src.File), src.Line))
	}
	return a
}

// trimPath keeps at most the last two path segments for a zap-style short caller.
func trimPath(file string) string {
	idx := strings.LastIndexByte(file, '/')
	if idx == -1 {
		return file
	}
	if idx2 := strings.LastIndexByte(file[:idx], '/'); idx2 != -1 {
		return file[idx2+1:]
	}
	return file
}
