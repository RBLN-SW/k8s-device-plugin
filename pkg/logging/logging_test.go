package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"
)

// mustLogger fails the test on invalid input — the test-side equivalent of the
// validation SetupFromEnv does before newLogger ever sees a level or format.
func mustLogger(t *testing.T, w io.Writer, level, format string) *slog.Logger {
	t.Helper()
	lvl, err := parseLevel(level)
	if err != nil {
		t.Fatalf("parseLevel(%q): %v", level, err)
	}
	f, err := parseFormat(format)
	if err != nil {
		t.Fatalf("parseFormat(%q): %v", format, err)
	}
	return newLogger(w, lvl, f)
}

func logLine(t *testing.T, level, format, emit string) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	logger := mustLogger(t, &buf, level, format)
	switch emit {
	case "info":
		logger.Info("Started component", "port", 8080)
	case "debug":
		logger.Debug("Polled daemon", "count", 3)
	}
	if buf.Len() == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %v: %s", err, buf.String())
	}
	return m
}

func TestLoggerDefaultsToInfoJSONWithNormalizedKeys(t *testing.T) {
	m := logLine(t, "", "", "info")
	if m == nil {
		t.Fatal("info line suppressed at default level")
	}
	if m["level"] != "info" {
		t.Fatalf("level = %v, want info (lowercase)", m["level"])
	}
	if m["msg"] != "Started component" {
		t.Fatalf("msg = %v", m["msg"])
	}
	if _, ok := m["ts"].(string); !ok {
		t.Fatalf("ts missing or not string: %v", m["ts"])
	}
	if !strings.Contains(m["ts"].(string), "T") {
		t.Fatalf("ts not RFC3339: %v", m["ts"])
	}
	if _, ok := m["caller"]; ok {
		t.Fatal("caller must be absent at info level")
	}
}

func TestLoggerGatesDebugAtInfo(t *testing.T) {
	if m := logLine(t, "info", "json", "debug"); m != nil {
		t.Fatalf("debug line leaked at info level: %v", m)
	}
}

func TestParseLevelRejectsTrace(t *testing.T) {
	if _, err := parseLevel("trace"); err == nil {
		t.Fatal("want error: this component logs nothing below debug")
	}
}

func TestParseRejectsUnknownLevelAndFormat(t *testing.T) {
	if _, err := parseLevel("loud"); err == nil {
		t.Fatal("want error for unknown level")
	}
	if _, err := parseFormat("yaml"); err == nil {
		t.Fatal("want error for unknown format")
	}
}

func TestLoggerPassesThroughUserTimeAttr(t *testing.T) {
	var buf bytes.Buffer
	logger := mustLogger(t, &buf, "info", "json")
	logger.Info("Measured duration", "time", "1.5s")
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %v: %s", err, buf.String())
	}
	if m["time"] != "1.5s" {
		t.Fatalf(`user "time" attr = %v, want "1.5s"`, m["time"])
	}
}

func TestLoggerTextFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := mustLogger(t, &buf, "", "text")
	logger.Info("Started component", "port", 8080)
	out := buf.String()
	for _, want := range []string{"level=info", `msg="Started component"`, "ts=", "port=8080"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text output missing %q: %s", want, out)
		}
	}
}

// Both "warning" and the output spelling "warn" configure the warn gate;
// output always spells "warn".
func TestLoggerWarnLevelInputAliasesAndOutputSpelling(t *testing.T) {
	for _, level := range []string{"warning", "warn"} {
		var buf bytes.Buffer
		logger := mustLogger(t, &buf, level, "json")
		logger.Warn("Request failed")
		var m map[string]any
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
			t.Fatalf("not JSON: %v: %s", err, buf.String())
		}
		if m["level"] != "warn" {
			t.Fatalf("level(%q) = %v, want warn (output spelling)", level, m["level"])
		}
	}
}

func TestLoggerCallerPresentAtDebugGate(t *testing.T) {
	m := logLine(t, "debug", "json", "info")
	if m == nil {
		t.Fatal("info line suppressed at debug level")
	}
	caller, ok := m["caller"].(string)
	if !ok {
		t.Fatal("caller must be present at debug gate")
	}
	if !regexp.MustCompile(`^[^/]+/[^/]+\.go:\d+$`).MatchString(caller) {
		t.Fatalf("caller = %q, want dir/file.go:N", caller)
	}
}

func TestTrimPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"c.go", "c.go"},
		{"b/c.go", "b/c.go"},
		{"a/b/c.go", "b/c.go"},
		{"x/a/b/c.go", "b/c.go"},
	} {
		if got := trimPath(tc.in); got != tc.want {
			t.Errorf("trimPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSetupFromEnvFallsBack(t *testing.T) {
	old := slog.Default()
	defer slog.SetDefault(old)
	t.Setenv(envLogLevel, "bogus")
	t.Setenv(envLogFormat, "json")
	settings := SetupFromEnv()
	if settings.Level != "info" || settings.Format != "json" {
		t.Fatalf("settings = %+v, want the info/json fallback", settings)
	}
	ctx := context.Background()
	if !slog.Default().Enabled(ctx, slog.LevelInfo) {
		t.Fatal("fallback logger must enable info")
	}
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		t.Fatal("fallback logger must gate debug (info default)")
	}
}

func TestSetupFromEnvInvalidLevelEmitsWarn(t *testing.T) {
	t.Setenv(envLogLevel, "bogus")
	t.Setenv(envLogFormat, "json")
	var buf bytes.Buffer
	setupFromEnv(&buf)
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("warn output not JSON: %v: %s", err, buf.String())
	}
	if m["msg"] != "Invalid "+envLogLevel+", using default" {
		t.Fatalf("msg = %v, want invalid-level warn", m["msg"])
	}
	if m["fallback"] != "info" {
		t.Fatalf("fallback = %v, want info", m["fallback"])
	}
}

func TestSetupFromEnvInvalidFormatFallsBackToJSON(t *testing.T) {
	t.Setenv(envLogLevel, "")
	t.Setenv(envLogFormat, "yaml")
	var buf bytes.Buffer
	logger, settings := setupFromEnv(&buf)
	if settings.Format != "json" {
		t.Fatalf("settings.Format = %q, want the json fallback", settings.Format)
	}
	// The warn itself must already be in the fallback format: JSON.
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("fallback output not JSON: %v: %s", err, buf.String())
	}
	if m["msg"] != "Invalid "+envLogFormat+", using default" {
		t.Fatalf("msg = %v, want invalid-format warn", m["msg"])
	}
	if m["fallback"] != "json" {
		t.Fatalf("fallback = %v, want json", m["fallback"])
	}
	buf.Reset()
	logger.Info("Started component")
	var next map[string]any
	if err := json.Unmarshal(buf.Bytes(), &next); err != nil {
		t.Fatalf("subsequent records not JSON: %v: %s", err, buf.String())
	}
}

// The error gate is chosen explicitly, so it also silences the fallback warns —
// the behaviour newLogger's caller documents.
func TestSetupFromEnvErrorLevelSuppressesFallbackWarn(t *testing.T) {
	t.Setenv(envLogLevel, "error")
	t.Setenv(envLogFormat, "yaml")
	var buf bytes.Buffer
	_, settings := setupFromEnv(&buf)
	if settings.Level != "error" || settings.Format != "json" {
		t.Fatalf("settings = %+v, want error/json", settings)
	}
	if buf.Len() != 0 {
		t.Fatalf("warn leaked past the error gate: %s", buf.String())
	}
}

func TestParseNormalizesCaseAndWhitespace(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"DEBUG", " debug ", "Debug"} {
		lvl, err := parseLevel(in)
		if err != nil {
			t.Fatalf("parseLevel(%q): %v", in, err)
		}
		if lvl != slog.LevelDebug {
			t.Fatalf("parseLevel(%q) = %v, want debug", in, lvl)
		}
	}
	for _, in := range []string{"TEXT", " text "} {
		format, err := parseFormat(in)
		if err != nil {
			t.Fatalf("parseFormat(%q): %v", in, err)
		}
		if format != "text" {
			t.Fatalf("parseFormat(%q) = %q, want text", in, format)
		}
	}
}
