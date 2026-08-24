package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	rblndevice "github.com/rbln-sw/rblnlib-go/pkg/device"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func inventoryDevice(name, health, statusName string) NPUDevice {
	return NPUDevice{
		Info:       rblndevice.Device{Name: name, ProductName: "RBLN-CA25", PCIBusID: "0000:4d:00.0"},
		Health:     health,
		StatusName: statusName,
	}
}

func TestDiffDeviceInventories(t *testing.T) {
	t.Parallel()

	ready := inventoryDevice("rbln0", pluginapi.Healthy, "READY")
	faulted := inventoryDevice("rbln0", pluginapi.Unhealthy, "FAULT")
	unreadable := inventoryDevice("rbln0", pluginapi.Healthy, deviceStatusUnreadable)
	sibling := inventoryDevice("rbln1", pluginapi.Healthy, "READY")

	tests := []struct {
		name        string
		previous    map[string]NPUDevice
		current     map[string]NPUDevice
		wantAdded   []string
		wantRemoved []string
		wantChanged []string
	}{
		{
			name:     "same devices in the same state",
			previous: map[string]NPUDevice{"rbln0": ready, "rbln1": sibling},
			current:  map[string]NPUDevice{"rbln0": ready, "rbln1": sibling},
		},
		{
			name:      "devices appear in sorted order",
			previous:  map[string]NPUDevice{},
			current:   map[string]NPUDevice{"rbln1": sibling, "rbln0": ready},
			wantAdded: []string{"rbln0", "rbln1"},
		},
		{
			name:        "device disappears",
			previous:    map[string]NPUDevice{"rbln0": ready, "rbln1": sibling},
			current:     map[string]NPUDevice{"rbln0": ready},
			wantRemoved: []string{"rbln1"},
		},
		{
			name:        "health flips",
			previous:    map[string]NPUDevice{"rbln0": ready},
			current:     map[string]NPUDevice{"rbln0": faulted},
			wantChanged: []string{"rbln0"},
		},
		{
			// Health stays Healthy because an unreadable status fails open, but
			// the status change is the only signal that sysfs went away.
			name:        "status changes while health holds",
			previous:    map[string]NPUDevice{"rbln0": ready},
			current:     map[string]NPUDevice{"rbln0": unreadable},
			wantChanged: []string{"rbln0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			delta := diffDeviceInventories(tc.previous, tc.current)

			if delta.isEmpty() != (len(tc.wantAdded)+len(tc.wantRemoved)+len(tc.wantChanged) == 0) {
				t.Fatalf("isEmpty() = %v for delta %+v", delta.isEmpty(), delta)
			}
			added := make([]string, 0, len(delta.Added))
			for _, device := range delta.Added {
				added = append(added, device.Info.Name)
			}
			changed := make([]string, 0, len(delta.Changed))
			for _, change := range delta.Changed {
				changed = append(changed, change.Device.Info.Name)
			}
			assertIDs(t, "added", added, tc.wantAdded)
			assertIDs(t, "removed", delta.Removed, tc.wantRemoved)
			assertIDs(t, "changed", changed, tc.wantChanged)
		})
	}
}

func assertIDs(t *testing.T, label string, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

func newTestPlugin(t *testing.T, devices map[string]NPUDevice) *ResourcePlugin {
	t.Helper()

	return NewResourcePlugin(
		"rebellions.ai/npu",
		filepath.Join(os.TempDir(), "rbln-inventory-test.sock"),
		filepath.Join(os.TempDir(), "kubelet-inventory-test.sock"),
		nil,
		devices,
	)
}

// A scan that finds no change must not restate the inventory: the loop runs
// every scan interval for the process's lifetime.
func TestUpdateDevicesStaysSilentWithoutChanges(t *testing.T) {
	devices := map[string]NPUDevice{"rbln0": inventoryDevice("rbln0", pluginapi.Healthy, "READY")}

	buf := captureLogs(t, slog.LevelInfo)
	plugin := newTestPlugin(t, devices)
	plugin.UpdateDevices(devices)

	if buf.Len() != 0 {
		t.Fatalf("unchanged scan logged: %s", buf.String())
	}
}

func TestUpdateDevicesReportsStateChangeWithCurrentCounts(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)
	plugin := newTestPlugin(t, map[string]NPUDevice{
		"rbln0": inventoryDevice("rbln0", pluginapi.Healthy, "READY"),
		"rbln1": inventoryDevice("rbln1", pluginapi.Healthy, "READY"),
	})

	plugin.UpdateDevices(map[string]NPUDevice{
		"rbln0": inventoryDevice("rbln0", pluginapi.Healthy, "READY"),
		"rbln1": inventoryDevice("rbln1", pluginapi.Unhealthy, "FAULT"),
	})

	records := decodeRecords(t, buf)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d: %s", len(records), buf.String())
	}
	record := records[0]
	if record["msg"] != "Device state changed" {
		t.Fatalf("msg = %v", record["msg"])
	}
	if record["level"] != "WARN" {
		t.Fatalf("level = %v, want WARN for a device turning unhealthy", record["level"])
	}
	for key, want := range map[string]any{
		"resourceName":   "rebellions.ai/npu",
		"device":         "rbln1",
		"health":         pluginapi.Unhealthy,
		"status":         "FAULT",
		"previousHealth": pluginapi.Healthy,
		"previousStatus": "READY",
		"deviceCount":    float64(2),
		"healthyCount":   float64(1),
		"unhealthyCount": float64(1),
	} {
		if record[key] != want {
			t.Fatalf("%s = %v, want %v (record: %v)", key, record[key], want, record)
		}
	}
}

func TestUpdateDevicesReportsAppearanceAndDisappearance(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)
	plugin := newTestPlugin(t, map[string]NPUDevice{
		"rbln0": inventoryDevice("rbln0", pluginapi.Healthy, "READY"),
	})

	plugin.UpdateDevices(map[string]NPUDevice{
		"rbln1": inventoryDevice("rbln1", pluginapi.Healthy, "READY"),
	})

	records := decodeRecords(t, buf)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d: %s", len(records), buf.String())
	}
	if records[0]["msg"] != "Device appeared in inventory" || records[0]["device"] != "rbln1" {
		t.Fatalf("first record = %v", records[0])
	}
	if records[1]["msg"] != "Device disappeared from inventory; it is no longer allocatable" {
		t.Fatalf("second record msg = %v", records[1]["msg"])
	}
	if records[1]["level"] != "WARN" || records[1]["device"] != "rbln0" {
		t.Fatalf("second record = %v", records[1])
	}
}

// A device that shows up already faulted is as unusable as one that faults
// later, so it must not be reported at a level an operator alerting on warn
// filters away.
func TestUpdateDevicesReportsUnhealthyAppearanceAtWarn(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)
	plugin := newTestPlugin(t, map[string]NPUDevice{})

	plugin.UpdateDevices(map[string]NPUDevice{
		"rbln0": inventoryDevice("rbln0", pluginapi.Unhealthy, "FAULT"),
	})

	records := decodeRecords(t, buf)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d: %s", len(records), buf.String())
	}
	if records[0]["msg"] != "Device appeared in inventory" || records[0]["level"] != "WARN" {
		t.Fatalf("record = %v", records[0])
	}
}

// Every record about a device carries the same identity keys, so one query
// answers "what happened to this card" across registration and hotplug.
func TestDeviceRecordsShareTheSameIdentityKeys(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)
	plugin := newTestPlugin(t, map[string]NPUDevice{})

	plugin.UpdateDevices(map[string]NPUDevice{
		"rbln0": inventoryDevice("rbln0", pluginapi.Healthy, "READY"),
	})

	records := decodeRecords(t, buf)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d: %s", len(records), buf.String())
	}
	for _, key := range []string{"device", "product", "pciDeviceID", "pciBusID", "numa", "health", "status"} {
		if _, ok := records[0][key]; !ok {
			t.Fatalf("record is missing %q: %v", key, records[0])
		}
	}
}

func TestAllocateReportsFailureWithDuration(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)
	plugin := newTestPlugin(t, map[string]NPUDevice{
		"rbln0": inventoryDevice("rbln0", pluginapi.Healthy, "READY"),
	})

	_, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"rbln9"}}},
	})
	if err == nil {
		t.Fatal("allocating an unmanaged device must fail")
	}

	records := decodeRecords(t, buf)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d: %s", len(records), buf.String())
	}
	if records[0]["msg"] != "Container allocation failed" || records[0]["level"] != "ERROR" {
		t.Fatalf("record = %v", records[0])
	}
	// The elapsed time is what separates a rejected request from one that ate
	// the kubelet's Allocate deadline.
	if _, ok := records[0]["durationMs"].(float64); !ok {
		t.Fatalf("record has no durationMs: %v", records[0])
	}
}

// Losing topology-aware placement is a silent performance regression, so it
// must reach the default gate rather than sit at info with the flow tracing.
func TestGetPreferredAllocationReportsFallbackAtWarn(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)
	plugin := newTestPlugin(t, map[string]NPUDevice{
		"rbln0": inventoryDevice("rbln0", pluginapi.Healthy, "READY"),
	})

	response, err := plugin.GetPreferredAllocation(context.Background(), &pluginapi.PreferredAllocationRequest{
		ContainerRequests: []*pluginapi.ContainerPreferredAllocationRequest{{
			AvailableDeviceIDs: nil,
			AllocationSize:     1,
		}},
	})
	if err != nil {
		t.Fatalf("the fallback must not fail the request: %v", err)
	}
	if len(response.ContainerResponses) != 1 || response.ContainerResponses[0].DeviceIDs != nil {
		t.Fatalf("fallback must leave the choice to kubelet: %v", response.ContainerResponses)
	}

	records := decodeRecords(t, buf)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d: %s", len(records), buf.String())
	}
	if records[0]["msg"] != "Preferred allocation fallback to kubelet" || records[0]["level"] != "WARN" {
		t.Fatalf("record = %v", records[0])
	}
}
