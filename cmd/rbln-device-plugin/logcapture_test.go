package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

// captureLogs replaces the process-wide default logger, so tests using it
// cannot run in parallel and must construct anything that binds a logger (for
// example NewResourcePlugin) *after* calling it.
func captureLogs(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})))

	return &buf
}

func decodeRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var records []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("captured output is not a JSON record stream: %v: %s", err, buf.String())
		}
		records = append(records, record)
	}

	return records
}
