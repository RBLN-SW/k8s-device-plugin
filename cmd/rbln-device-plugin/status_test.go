package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// The healthForDevice tests are not parallel: they point the package-level
// sysfs root at a temporary directory.
func TestHealthForDevice(t *testing.T) {
	root := t.TempDir()
	original := rebellionsSysfsClassPath
	t.Cleanup(func() { rebellionsSysfsClassPath = original })
	rebellionsSysfsClassPath = root

	tests := []struct {
		name       string
		device     string
		raw        string
		wantHealth string
		wantStatus string
	}{
		{name: "ready", device: "rbln0", raw: "0\n", wantHealth: pluginapi.Healthy, wantStatus: "READY"},
		{name: "busy", device: "rbln1", raw: "1", wantHealth: pluginapi.Unhealthy, wantStatus: "BUSY"},
		{name: "fault", device: "rbln2", raw: "3", wantHealth: pluginapi.Unhealthy, wantStatus: "FAULT"},
		{name: "unknown value", device: "rbln3", raw: "9", wantHealth: pluginapi.Unhealthy, wantStatus: "UNKNOWN(9)"},
		// A garbled status file must not drain allocatable devices.
		{name: "unparsable", device: "rbln4", raw: "n/a", wantHealth: pluginapi.Healthy, wantStatus: deviceStatusUnreadable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, tc.device)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("create device dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "status"), []byte(tc.raw), 0o600); err != nil {
				t.Fatalf("write status: %v", err)
			}

			health, status := healthForDevice(tc.device)
			if health != tc.wantHealth || status != tc.wantStatus {
				t.Fatalf("healthForDevice(%q) = (%q, %q), want (%q, %q)",
					tc.device, health, status, tc.wantHealth, tc.wantStatus)
			}
		})
	}
}

func TestHealthForDeviceFailsOpenWhenStatusIsMissing(t *testing.T) {
	original := rebellionsSysfsClassPath
	t.Cleanup(func() { rebellionsSysfsClassPath = original })
	rebellionsSysfsClassPath = t.TempDir()

	health, status := healthForDevice("rbln404")
	if health != pluginapi.Healthy {
		t.Fatalf("health = %q, want %q: an unreadable status must not remove capacity", health, pluginapi.Healthy)
	}
	if status != deviceStatusUnreadable {
		t.Fatalf("status = %q, want %q", status, deviceStatusUnreadable)
	}
}

// The read failure is diagnosable at debug, but must not reach the default gate:
// it recurs for every device on every scan.
func TestHealthForDeviceKeepsReadFailureBelowInfo(t *testing.T) {
	original := rebellionsSysfsClassPath
	t.Cleanup(func() { rebellionsSysfsClassPath = original })
	rebellionsSysfsClassPath = t.TempDir()

	buf := captureLogs(t, slog.LevelInfo)
	healthForDevice("rbln404")
	if buf.Len() != 0 {
		t.Fatalf("status read failure leaked into the info stream: %s", buf.String())
	}

	debugBuf := captureLogs(t, slog.LevelDebug)
	healthForDevice("rbln404")
	records := decodeRecords(t, debugBuf)
	if len(records) != 1 {
		t.Fatalf("expected 1 debug record, got %d: %s", len(records), debugBuf.String())
	}
	if records[0]["device"] != "rbln404" {
		t.Fatalf("debug record lacks the device key: %v", records[0])
	}
}
