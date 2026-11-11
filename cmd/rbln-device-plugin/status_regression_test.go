package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	rblndevice "github.com/rbln-sw/rblnlib-go/pkg/device"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
)

// These tests are not parallel: discovery and the sysfs roots are package seams.
type statusFixture struct {
	plugin   *ResourcePlugin
	manager  *Manager
	root     string
	status   string
	cdiRoot  string
	rsdCalls int
}

func newStatusFixture(t *testing.T, initialStatus string) *statusFixture {
	t.Helper()
	originalRoot := rebellionsSysfsClassPath
	originalPCI := sriovPCISysfsDevicesPath
	originalGetDevices := getDevices
	t.Cleanup(func() {
		rebellionsSysfsClassPath = originalRoot
		sriovPCISysfsDevicesPath = originalPCI
		getDevices = originalGetDevices
	})
	f := &statusFixture{root: t.TempDir(), cdiRoot: t.TempDir()}
	rebellionsSysfsClassPath = f.root
	sriovPCISysfsDevicesPath = t.TempDir()
	// /dev/null lets successful allocation exercise the complete path without hardware.
	device := rblndevice.Device{Name: "null", ProductName: "RBLN-CA25", PCIBusID: "0000:4d:00.0"}
	getDevices = func(context.Context) ([]rblndevice.Device, error) {
		return []rblndevice.Device{device}, nil
	}
	f.status = filepath.Join(f.root, device.Name, "status")
	if initialStatus != "" {
		f.writeStatus(t, initialStatus)
	}
	cdi, err := NewCDIHandler(f.cdiRoot)
	if err != nil {
		t.Fatal(err)
	}
	groups := discoverDeviceGroups(context.Background())
	f.plugin = NewResourcePlugin(consts.GenericResourceName, "", "", cdi, groups[consts.GenericResourceName].Devices)
	f.manager = &Manager{
		plugins: map[string]*ResourcePlugin{consts.GenericResourceName: f.plugin},
	}
	rsdPath := filepath.Join(t.TempDir(), "rsd0")
	if err := os.WriteFile(rsdPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.plugin.rsdGroupFn = func([]string) (string, error) {
		f.rsdCalls++
		return rsdPath, nil
	}
	return f
}

func (f *statusFixture) writeStatus(t *testing.T, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.status), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.status, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *statusFixture) breakStatus(t *testing.T, failure string) {
	t.Helper()
	if err := os.Remove(f.status); err != nil {
		t.Fatal(err)
	}
	switch failure {
	case "missing":
	case "read error":
		// EISDIR is deterministic even if the suite runs as root.
		if err := os.Mkdir(f.status, 0o755); err != nil {
			t.Fatal(err)
		}
	case "parse error":
		f.writeStatus(t, "not-a-number\n")
	default:
		t.Fatalf("unknown failure %q", failure)
	}
}

func (f *statusFixture) scan(t *testing.T, wantHealth string) {
	t.Helper()
	if err := f.manager.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	devices := f.plugin.currentDevices()
	if len(devices) != 1 || devices[0].Health != wantHealth {
		t.Fatalf("scan devices = %v, want one %s device", devices, wantHealth)
	}
	// ListAndWatch still gets its updates through the existing scan channel.
	select {
	case update := <-f.plugin.updateCh:
		if len(update) != 1 || update[0].Health != wantHealth {
			t.Fatalf("ListAndWatch update = %v, want %s", update, wantHealth)
		}
	default:
		t.Fatal("scan did not queue a ListAndWatch update")
	}
}

func (f *statusFixture) allocate(t *testing.T, wantBlocked bool) {
	t.Helper()
	before := f.rsdCalls
	response, err := f.plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"null"}}},
	})
	if wantBlocked {
		if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "unhealthy") {
			t.Errorf("Allocate error = %v, want unhealthy device rejection", err)
		}
		if response != nil {
			t.Error("rejected Allocate returned device specs or CDI annotations")
		}
		if f.rsdCalls != before {
			t.Errorf("rejected Allocate created %d RSD groups", f.rsdCalls-before)
		}
		entries, readErr := os.ReadDir(f.cdiRoot)
		if readErr != nil || len(entries) != 0 {
			t.Errorf("rejected Allocate changed CDI directory: %v, %v", entries, readErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if f.rsdCalls != before+1 || len(response.ContainerResponses) != 1 || len(response.ContainerResponses[0].Annotations) == 0 {
		t.Fatalf("successful Allocate did not create RSD/CDI response: %v", response)
	}
}

func TestStatusFaultSurvivesReadFailures(t *testing.T) {
	for _, failure := range []string{"missing", "read error", "parse error"} {
		t.Run(failure, func(t *testing.T) {
			f := newStatusFixture(t, "0\n")
			f.writeStatus(t, "4\n")
			f.scan(t, pluginapi.Unhealthy)
			f.breakStatus(t, failure)
			buf := captureLogs(t, slog.LevelDebug)
			for range 3 {
				f.allocate(t, true)
				f.scan(t, pluginapi.Unhealthy)
			}
			for _, record := range decodeRecords(t, buf) {
				if record["health"] == pluginapi.Healthy || strings.Contains(record["msg"].(string), "assuming healthy") {
					t.Errorf("read failure logged as recovery: %v", record)
				}
			}
			if err := os.Remove(f.status); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			f.writeStatus(t, "0\n")
			f.scan(t, pluginapi.Healthy)
			f.allocate(t, false)
			// Once READY has actually been observed, missing status can again
			// use the compatibility default.
			f.breakStatus(t, "missing")
			f.scan(t, pluginapi.Healthy)
			f.allocate(t, false)
		})
	}
}

func TestStatusAllocateFaultPersistsAcrossScanAndRetry(t *testing.T) {
	for _, failure := range []string{"missing", "read error", "parse error"} {
		t.Run(failure, func(t *testing.T) {
			f := newStatusFixture(t, "0\n")
			f.writeStatus(t, "4\n")
			f.allocate(t, true) // Fault appeared after the last scan.
			f.breakStatus(t, failure)
			for range 3 {
				f.allocate(t, true)
				f.scan(t, pluginapi.Unhealthy)
			}
			if err := os.Remove(f.status); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			f.writeStatus(t, "0\n")
			f.allocate(t, false) // Allocate can also observe real recovery.
			f.breakStatus(t, "missing")
			f.scan(t, pluginapi.Healthy)
			f.allocate(t, false)
		})
	}
}

func TestStatusMissingWithoutFaultHistoryAllowsAllocation(t *testing.T) {
	f := newStatusFixture(t, "")
	for range 3 {
		f.scan(t, pluginapi.Healthy)
		f.allocate(t, false)
	}
}

func TestStatusAllocateRejectsWholeRequestBeforeSideEffects(t *testing.T) {
	for _, separateContainers := range []bool{false, true} {
		name := "multiple devices"
		if separateContainers {
			name = "multiple containers"
		}
		t.Run(name, func(t *testing.T) {
			f := newStatusFixture(t, "0")
			f.plugin.UpdateDevices(map[string]NPUDevice{
				"null": inventoryDevice("null", pluginapi.Healthy, "READY"),
				"zero": inventoryDevice("zero", pluginapi.Healthy, "READY"),
			})
			f.writeStatus(t, "4") // The advertised snapshot is still Healthy.
			requests := []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"zero", "null"}}}
			if separateContainers {
				requests = []*pluginapi.ContainerAllocateRequest{
					{DevicesIds: []string{"zero"}},
					{DevicesIds: []string{"null"}},
				}
			}
			response, err := f.plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{ContainerRequests: requests})
			if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "unhealthy (status FAULT)") {
				t.Errorf("Allocate error = %v, want unhealthy FAULT rejection", err)
			}
			if response != nil || f.rsdCalls != 0 {
				t.Errorf("rejected request produced side effects: response=%v, RSD calls=%d", response, f.rsdCalls)
			}
		})
	}
}

func TestStatusAllocateReadsEachRequestedDeviceOnce(t *testing.T) {
	f := newStatusFixture(t, "")
	f.plugin.UpdateDevices(map[string]NPUDevice{
		"null":        inventoryDevice("null", pluginapi.Healthy, "READY"),
		"zero":        inventoryDevice("zero", pluginapi.Healthy, "READY"),
		"unrequested": inventoryDevice("unrequested", pluginapi.Healthy, "READY"),
	})
	discover := getDevices
	getDevices = func(ctx context.Context) ([]rblndevice.Device, error) {
		t.Error("Allocate must not rediscover the full inventory")
		return discover(ctx)
	}
	buf := captureLogs(t, slog.LevelDebug)
	response, err := f.plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{DevicesIds: []string{"zero"}},
			{DevicesIds: []string{"null"}},
			{DevicesIds: []string{"null"}}, // Reuse the observation for repeated IDs.
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.rsdCalls != 3 || len(response.ContainerResponses) != 3 {
		t.Fatalf("expected three allocations: RSD calls=%d, response=%v", f.rsdCalls, response)
	}
	for i, wantPath := range []string{"/dev/zero", "/dev/null", "/dev/null"} {
		devices := response.ContainerResponses[i].Devices
		if len(devices) != 2 || devices[1].HostPath != wantPath {
			t.Errorf("container %d devices = %v, want RSD and %s", i, devices, wantPath)
		}
	}
	// Missing status emits one debug record per actual read. This checks both
	// cross-container deduplication and absence of a second allocation read.
	reads := make(map[string]int)
	for _, record := range decodeRecords(t, buf) {
		if record["msg"] == "Failed to read device status; retaining health" {
			reads[record["device"].(string)]++
		}
	}
	if len(reads) != 2 || reads["zero"] != 1 || reads["null"] != 1 {
		t.Fatalf("status reads = %v, want exactly one per requested device", reads)
	}
}

func TestStatusStaleDiscoveryCannotClearAllocateFault(t *testing.T) {
	f := newStatusFixture(t, "0")
	groups := discoverDeviceGroups(context.Background())
	f.writeStatus(t, "4")
	f.allocate(t, true)
	f.breakStatus(t, "missing")
	f.plugin.UpdateDevices(groups[consts.GenericResourceName].Devices)
	if devices := f.plugin.currentDevices(); len(devices) != 1 || devices[0].Health != pluginapi.Unhealthy {
		t.Fatalf("stale discovery cleared Allocate fault: %v", devices)
	}
	f.allocate(t, true)
}

func TestStatusHistoryLivesForResourcePluginInstance(t *testing.T) {
	f := newStatusFixture(t, "4")
	f.allocate(t, true)
	f.breakStatus(t, "missing")
	devices := cloneDeviceMap(f.plugin.devices)
	f.plugin.UpdateDevices(nil)
	f.plugin.UpdateDevices(devices)
	f.allocate(t, true)

	// Individual disappearance does not reset history, but a replacement
	// plugin (as on kubelet/plugin restart) begins with no remembered fault.
	replacement := NewResourcePlugin(consts.GenericResourceName, "", "", f.plugin.cdi, devices)
	if _, err := replacement.selectedDevices([]string{"null"}); err != nil {
		t.Fatalf("new instance should use the no-history compatibility default: %v", err)
	}
}

func TestStatusConcurrentScanAndAllocateRetainFault(t *testing.T) {
	f := newStatusFixture(t, "4")
	f.breakStatus(t, "missing")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 25 {
			f.scan(t, pluginapi.Unhealthy)
		}
	}()
	go func() {
		defer wg.Done()
		for range 25 {
			f.allocate(t, true)
		}
	}()
	wg.Wait()
}
