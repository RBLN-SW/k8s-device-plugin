package main

import (
	"errors"
	"log/slog"
	"testing"
)

// An unreachable collector must be visible in the structured stream at the
// default gate, and must not claim a device or a request is unusable.
func TestOTELErrorHandlerReportsAtWarn(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)

	otelErrorHandler{}.Handle(errors.New("traces export: connection refused"))

	records := decodeRecords(t, buf)
	if len(records) != 1 {
		t.Fatalf("expected 1 record past the info gate, got %d: %s", len(records), buf.String())
	}
	if records[0]["level"] != "WARN" || records[0]["msg"] != "OpenTelemetry SDK error" {
		t.Fatalf("error handler record = %v", records[0])
	}
	if records[0]["err"] != "traces export: connection refused" {
		t.Fatalf("error is not under the err key: %v", records[0])
	}
	if records[0]["component"] != "otel" {
		t.Fatalf("record is not attributed to otel: %v", records[0])
	}
}

// The SDK's own diagnostics must not reach the default gate, while the errors
// it reports must — a malformed OTLP endpoint is how this misconfigures.
func TestOTELLogSinkMapsSeveritiesAndTagsComponent(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)

	sink := otelLogSink{}
	sink.Info(4, "span batch exported")
	sink.Error(errors.New("missing protocol scheme"), "parse url", "input", "://bad")

	records := decodeRecords(t, buf)
	if len(records) != 1 {
		t.Fatalf("expected 1 record past the info gate, got %d: %s", len(records), buf.String())
	}
	if records[0]["level"] != "WARN" || records[0]["msg"] != "parse url" {
		t.Fatalf("sink error record = %v", records[0])
	}
	if records[0]["input"] != "://bad" || records[0]["err"] != "missing protocol scheme" {
		t.Fatalf("sink error dropped its key-values: %v", records[0])
	}
	if records[0]["component"] != "otel" {
		t.Fatalf("record is not attributed to otel: %v", records[0])
	}
}

func TestOTELLogSinkVerbosityFollowsDebugGate(t *testing.T) {
	sink := otelLogSink{}

	captureLogs(t, slog.LevelInfo)
	if sink.Enabled(0) {
		t.Fatal("SDK verbose logging must be off at the info gate")
	}

	buf := captureLogs(t, slog.LevelDebug)
	if !sink.Enabled(0) {
		t.Fatal("SDK verbose logging must follow the debug gate")
	}

	sink.WithValues("exporter", "otlp").Info(4, "span batch exported")
	records := decodeRecords(t, buf)
	if len(records) != 1 || records[0]["level"] != "DEBUG" {
		t.Fatalf("info records must land at debug: %s", buf.String())
	}
	// WithValues is how logr carries context down; dropping it would silently
	// strip the SDK's own attribution from every record below it.
	if records[0]["exporter"] != "otlp" {
		t.Fatalf("WithValues attributes did not survive: %v", records[0])
	}
}
