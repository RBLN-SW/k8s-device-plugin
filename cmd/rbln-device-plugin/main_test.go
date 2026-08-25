package main

import (
	"io"
	"os"
	"testing"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/logging"
)

// TestNewAppRunsHelpWithoutPanic guards against flag wiring regressions
// causing a startup panic instead of a normal --help exit.
func TestNewAppRunsHelpWithoutPanic(t *testing.T) {
	t.Parallel()

	app := newApp(logging.Settings{Level: "info", Format: "json"})
	app.Writer = io.Discard

	if err := app.Run([]string{"rbln-device-plugin", "--help"}); err != nil {
		t.Fatalf("run --help: %v", err)
	}
}

// Usage output must not share the stream carrying the log records: on a flag
// error cli prints "Incorrect Usage" plus the entire help text through Writer,
// which on stdout is a dozen unparseable lines in the middle of the JSON.
func TestNewAppKeepsUsageOutputOffTheLogStream(t *testing.T) {
	t.Parallel()

	app := newApp(logging.Settings{Level: "info", Format: "json"})

	if app.Writer == os.Stdout {
		t.Fatal("app.Writer is stdout; a flag error would corrupt the log stream")
	}
	if app.ErrWriter == os.Stdout {
		t.Fatal("app.ErrWriter is stdout; a flag error would corrupt the log stream")
	}
}

// A DaemonSet passing klog's "-v 2" must fail, not print the version and exit
// 0. Succeeding silently is the one failure mode no log stream can report.
func TestNewAppRejectsKlogVerbosityArgs(t *testing.T) {
	for _, args := range [][]string{
		{"rbln-device-plugin", "-v", "2"},
		{"rbln-device-plugin", "-v=2"},
	} {
		app := newApp(logging.Settings{Level: "info", Format: "json"})
		app.Writer = io.Discard
		app.ErrWriter = io.Discard

		if err := app.Run(args); err == nil {
			t.Fatalf("%v exited 0; a wrong arg must not look like success", args[1:])
		}
	}
}
