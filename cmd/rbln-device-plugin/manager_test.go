/*
Copyright 2026 Rebellions Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	rblndevice "github.com/rbln-sw/rblnlib-go/pkg/device"
)

// The info stream reports only change, so a steady-state node logs nothing for
// as long as it stays healthy. At debug the scan must still be observable:
// "is the loop alive, and how long does a scan take" is otherwise unanswerable.
func TestReconcileReportsScanAtDebug(t *testing.T) {
	originalGetDevices := getDevices
	t.Cleanup(func() { getDevices = originalGetDevices })
	getDevices = func(context.Context) ([]rblndevice.Device, error) {
		return nil, errors.New("discovery unavailable")
	}

	buf := captureLogs(t, slog.LevelDebug)
	manager := &Manager{
		config:  &Config{flags: &Flags{deviceScanInterval: time.Minute}},
		plugins: make(map[string]*ResourcePlugin),
	}

	if err := manager.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var scan map[string]any
	for _, record := range decodeRecords(t, buf) {
		if record["msg"] == "Reconciled device inventory" {
			scan = record
		}
	}
	if scan == nil {
		t.Fatalf("no scan record: %s", buf.String())
	}
	if scan["level"] != "DEBUG" {
		t.Fatalf("level = %v, want DEBUG: a per-scan record must not reach the default gate", scan["level"])
	}
	for _, key := range []string{"resourceCount", "deviceCount", "durationMs"} {
		if _, ok := scan[key].(float64); !ok {
			t.Fatalf("scan record is missing %q: %v", key, scan)
		}
	}
}
