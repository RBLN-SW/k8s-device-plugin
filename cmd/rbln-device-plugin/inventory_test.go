package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	rblndevice "github.com/rbln-sw/rblnlib-go/pkg/device"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
)

func TestResourceForDevice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		device     rblndevice.Device
		sriov      sriovInfo
		useGeneric bool
		expected   string
		advertise  bool
		wantErr    bool
	}{
		{
			name:      "vf in vf-4 mode",
			device:    rblndevice.Device{Name: "rbln0-0", ProductName: "RBLN-CR03"},
			sriov:     sriovInfo{class: sriovClassVF, parentPFBusID: "0000:17:00.0", numVFs: 4},
			expected:  "rebellions.ai/npu-vf4",
			advertise: true,
		},
		{
			name:       "vf naming ignores generic flag",
			device:     rblndevice.Device{Name: "rbln0-0", ProductName: "RBLN-CR03"},
			sriov:      sriovInfo{class: sriovClassVF, parentPFBusID: "0000:17:00.0", numVFs: 4},
			useGeneric: true,
			expected:   "rebellions.ai/npu-vf4",
			advertise:  true,
		},
		{
			name:      "vf in vf-1 mode",
			device:    rblndevice.Device{Name: "rbln0-0", ProductName: "RBLN-CR03"},
			sriov:     sriovInfo{class: sriovClassVF, parentPFBusID: "0000:17:00.0", numVFs: 1},
			expected:  "rebellions.ai/npu-vf1",
			advertise: true,
		},
		{
			name:    "vf in unsupported partition mode",
			device:  rblndevice.Device{Name: "rbln0-0", ProductName: "RBLN-CR03"},
			sriov:   sriovInfo{class: sriovClassVF, parentPFBusID: "0000:17:00.0", numVFs: 2},
			wantErr: true,
		},
		{
			name:   "vf-hosting pf is not advertised",
			device: rblndevice.Device{Name: "rbln0", ProductName: "RBLN-CR03"},
			sriov:  sriovInfo{class: sriovClassHostingPF, numVFs: 4},
		},
		{
			name:       "plain pf generic mode",
			device:     rblndevice.Device{Name: "rbln0", ProductName: "RBLN-CA25"},
			sriov:      sriovInfo{class: sriovClassNone},
			useGeneric: true,
			expected:   consts.GenericResourceName,
			advertise:  true,
		},
		{
			name:      "plain pf atom",
			device:    rblndevice.Device{Name: "rbln0", ProductName: "RBLN-CA25"},
			sriov:     sriovInfo{class: sriovClassNone},
			expected:  consts.AtomResourceName,
			advertise: true,
		},
		{
			name:      "plain pf rebel",
			device:    rblndevice.Device{Name: "rbln0", ProductName: "RBLN-CR03"},
			sriov:     sriovInfo{class: sriovClassNone},
			expected:  consts.RebelResourceName,
			advertise: true,
		},
		{
			name:    "plain pf unsupported product",
			device:  rblndevice.Device{Name: "rbln0", ProductName: "RBLN-XX01"},
			sriov:   sriovInfo{class: sriovClassNone},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			actual, advertise, err := resourceForDevice(tc.device, tc.sriov, tc.useGeneric)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q advertise=%v", actual, advertise)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if advertise != tc.advertise {
				t.Fatalf("expected advertise=%v, got %v", tc.advertise, advertise)
			}
			if actual != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, actual)
			}
		})
	}
}

// The discoverDeviceGroups tests are not parallel: they mutate the shared
// package-level getDevices seam.
func TestDiscoverDeviceGroups(t *testing.T) {
	originalGetDevices := getDevices
	t.Cleanup(func() {
		getDevices = originalGetDevices
	})

	getDevices = func(context.Context) ([]rblndevice.Device, error) {
		return []rblndevice.Device{
			{Name: "rbln0", ProductName: "RBLN-CA25"},
			{Name: "rbln1", ProductName: "RBLN-CR03"},
		}, nil
	}

	groups := discoverDeviceGroups(context.Background(), false)
	if len(groups) != 2 {
		t.Fatalf("expected 2 legacy groups, got %d", len(groups))
	}
	if _, ok := groups[consts.AtomResourceName].Devices["rbln0"]; !ok {
		t.Fatalf("missing ATOM device group entry")
	}
	if _, ok := groups[consts.RebelResourceName].Devices["rbln1"]; !ok {
		t.Fatalf("missing REBEL device group entry")
	}

	groups = discoverDeviceGroups(context.Background(), true)
	group, ok := groups[consts.GenericResourceName]
	if !ok {
		t.Fatalf("missing generic resource group")
	}
	if len(group.Devices) != 2 {
		t.Fatalf("expected 2 devices in generic group, got %d", len(group.Devices))
	}
}

func TestDiscoverDeviceGroupsReportsZeroDevicesOnError(t *testing.T) {
	originalGetDevices := getDevices
	t.Cleanup(func() {
		getDevices = originalGetDevices
	})

	getDevices = func(context.Context) ([]rblndevice.Device, error) {
		return nil, errors.New("rbln-smi timed out")
	}

	groups := discoverDeviceGroups(context.Background(), false)
	if len(groups) != 0 {
		t.Fatalf("expected zero device groups on discovery error, got %d", len(groups))
	}
}

func TestDiscoverDeviceGroupsSkipsUnsupportedProduct(t *testing.T) {
	originalGetDevices := getDevices
	t.Cleanup(func() {
		getDevices = originalGetDevices
	})

	getDevices = func(context.Context) ([]rblndevice.Device, error) {
		return []rblndevice.Device{
			{Name: "rbln0", ProductName: "RBLN-CA25"},
			{Name: "rbln1", ProductName: "RBLN-XX01"},
		}, nil
	}

	groups := discoverDeviceGroups(context.Background(), false)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group with the unsupported device skipped, got %d", len(groups))
	}
	atom, ok := groups[consts.AtomResourceName]
	if !ok {
		t.Fatalf("missing ATOM device group entry")
	}
	if _, ok := atom.Devices["rbln0"]; !ok {
		t.Fatalf("supported sibling device must survive the skip")
	}
	if _, ok := atom.Devices["rbln1"]; ok {
		t.Fatalf("unsupported device must not be exposed")
	}
}

// The SR-IOV discovery tests are not parallel: they mutate the shared
// package-level getDevices and sriovPCISysfsDevicesPath seams.
func setupSRIOVDiscovery(t *testing.T) string {
	t.Helper()

	originalGetDevices := getDevices
	originalSysfsPath := sriovPCISysfsDevicesPath
	t.Cleanup(func() {
		getDevices = originalGetDevices
		sriovPCISysfsDevicesPath = originalSysfsPath
	})

	root := t.TempDir()
	sriovPCISysfsDevicesPath = root
	return root
}

func vfDeviceForTest(name, busID string) rblndevice.Device {
	return rblndevice.Device{Name: name, ProductName: "RBLN-CR03", PCIBusID: busID}
}

func TestDiscoverDeviceGroupsVF4Node(t *testing.T) {
	root := setupSRIOVDiscovery(t)

	writeSysfsAttr(t, root, "0000:17:00.0", "sriov_numvfs", "4\n")
	writeSysfsAttr(t, root, "0000:38:00.0", "sriov_numvfs", "4\n")
	for i := 1; i <= 4; i++ {
		writeSysfsSymlink(t, root, fmt.Sprintf("0000:17:00.%d", i), "physfn", "../0000:17:00.0")
		writeSysfsSymlink(t, root, fmt.Sprintf("0000:38:00.%d", i), "physfn", "../0000:38:00.0")
	}

	getDevices = func(context.Context) ([]rblndevice.Device, error) {
		return []rblndevice.Device{
			// The VF-hosting PFs may or may not be reported by rbln-smi; list
			// them here to prove they are excluded either way.
			vfDeviceForTest("rbln0", "0000:17:00.0"),
			vfDeviceForTest("rbln1", "0000:38:00.0"),
			vfDeviceForTest("rbln0-0", "0000:17:00.1"),
			vfDeviceForTest("rbln0-1", "0000:17:00.2"),
			vfDeviceForTest("rbln0-2", "0000:17:00.3"),
			vfDeviceForTest("rbln0-3", "0000:17:00.4"),
			vfDeviceForTest("rbln1-0", "0000:38:00.1"),
			vfDeviceForTest("rbln1-1", "0000:38:00.2"),
			vfDeviceForTest("rbln1-2", "0000:38:00.3"),
			vfDeviceForTest("rbln1-3", "0000:38:00.4"),
		}, nil
	}

	groups := discoverDeviceGroups(context.Background(), true)
	if len(groups) != 1 {
		t.Fatalf("expected only the VF group, got %d groups: %v", len(groups), groupNames(groups))
	}
	if _, ok := groups[consts.GenericResourceName]; ok {
		t.Fatalf("VF-hosting PFs must not surface a %s group", consts.GenericResourceName)
	}
	vfGroup, ok := groups["rebellions.ai/npu-vf4"]
	if !ok {
		t.Fatalf("missing rebellions.ai/npu-vf4 group, got %v", groupNames(groups))
	}
	if len(vfGroup.Devices) != 8 {
		t.Fatalf("expected 8 VF devices, got %d", len(vfGroup.Devices))
	}
	if got := vfGroup.Devices["rbln0-0"].ParentPFBusID; got != "0000:17:00.0" {
		t.Fatalf("expected parent PF 0000:17:00.0 for rbln0-0, got %q", got)
	}
	if got := vfGroup.Devices["rbln1-3"].ParentPFBusID; got != "0000:38:00.0" {
		t.Fatalf("expected parent PF 0000:38:00.0 for rbln1-3, got %q", got)
	}
}

func TestDiscoverDeviceGroupsVF1Node(t *testing.T) {
	root := setupSRIOVDiscovery(t)

	writeSysfsAttr(t, root, "0000:17:00.0", "sriov_numvfs", "1\n")
	writeSysfsAttr(t, root, "0000:38:00.0", "sriov_numvfs", "1\n")
	writeSysfsSymlink(t, root, "0000:17:00.1", "physfn", "../0000:17:00.0")
	writeSysfsSymlink(t, root, "0000:38:00.1", "physfn", "../0000:38:00.0")

	getDevices = func(context.Context) ([]rblndevice.Device, error) {
		return []rblndevice.Device{
			vfDeviceForTest("rbln0-0", "0000:17:00.1"),
			vfDeviceForTest("rbln1-0", "0000:38:00.1"),
		}, nil
	}

	groups := discoverDeviceGroups(context.Background(), true)
	if len(groups) != 1 {
		t.Fatalf("expected only the VF group, got %d groups: %v", len(groups), groupNames(groups))
	}
	vfGroup, ok := groups["rebellions.ai/npu-vf1"]
	if !ok {
		t.Fatalf("missing rebellions.ai/npu-vf1 group, got %v", groupNames(groups))
	}
	if len(vfGroup.Devices) != 2 {
		t.Fatalf("expected 1 VF per PF (2 total), got %d", len(vfGroup.Devices))
	}
}

func TestDiscoverDeviceGroupsExcludesUnsupportedVFCount(t *testing.T) {
	root := setupSRIOVDiscovery(t)

	writeSysfsAttr(t, root, "0000:17:00.0", "sriov_numvfs", "2\n")
	writeSysfsSymlink(t, root, "0000:17:00.1", "physfn", "../0000:17:00.0")
	writeSysfsSymlink(t, root, "0000:17:00.2", "physfn", "../0000:17:00.0")

	getDevices = func(context.Context) ([]rblndevice.Device, error) {
		return []rblndevice.Device{
			vfDeviceForTest("rbln0", "0000:17:00.0"),
			vfDeviceForTest("rbln0-0", "0000:17:00.1"),
			vfDeviceForTest("rbln0-1", "0000:17:00.2"),
		}, nil
	}

	groups := discoverDeviceGroups(context.Background(), true)
	if len(groups) != 0 {
		t.Fatalf("expected no groups for an unsupported vf-2 node, got %v", groupNames(groups))
	}
}

func TestDiscoverDeviceGroupsMixedNode(t *testing.T) {
	root := setupSRIOVDiscovery(t)

	writeSysfsAttr(t, root, "0000:17:00.0", "sriov_numvfs", "4\n")
	for i := 1; i <= 4; i++ {
		writeSysfsSymlink(t, root, fmt.Sprintf("0000:17:00.%d", i), "physfn", "../0000:17:00.0")
	}
	// Non-partitioned PF: SR-IOV capable but zero VFs.
	writeSysfsAttr(t, root, "0000:38:00.0", "sriov_numvfs", "0\n")

	getDevices = func(context.Context) ([]rblndevice.Device, error) {
		return []rblndevice.Device{
			vfDeviceForTest("rbln0", "0000:17:00.0"),
			vfDeviceForTest("rbln0-0", "0000:17:00.1"),
			vfDeviceForTest("rbln0-1", "0000:17:00.2"),
			vfDeviceForTest("rbln0-2", "0000:17:00.3"),
			vfDeviceForTest("rbln0-3", "0000:17:00.4"),
			vfDeviceForTest("rbln1", "0000:38:00.0"),
		}, nil
	}

	groups := discoverDeviceGroups(context.Background(), false)
	if len(groups) != 2 {
		t.Fatalf("expected VF and REBEL groups, got %v", groupNames(groups))
	}
	vfGroup, ok := groups["rebellions.ai/npu-vf4"]
	if !ok {
		t.Fatalf("missing rebellions.ai/npu-vf4 group, got %v", groupNames(groups))
	}
	if len(vfGroup.Devices) != 4 {
		t.Fatalf("expected 4 VF devices, got %d", len(vfGroup.Devices))
	}
	if _, ok := vfGroup.Devices["rbln0"]; ok {
		t.Fatalf("VF-hosting PF must not appear in the VF group")
	}
	rebelGroup, ok := groups[consts.RebelResourceName]
	if !ok {
		t.Fatalf("missing REBEL group for the non-partitioned PF, got %v", groupNames(groups))
	}
	if len(rebelGroup.Devices) != 1 {
		t.Fatalf("expected 1 non-partitioned PF, got %d", len(rebelGroup.Devices))
	}
	if _, ok := rebelGroup.Devices["rbln1"]; !ok {
		t.Fatalf("non-partitioned PF must keep its existing resource name")
	}
}

func TestDiscoverDeviceGroupsExcludesDeviceOnSysfsAnomaly(t *testing.T) {
	root := setupSRIOVDiscovery(t)

	// physfn exists but the parent PF has no readable sriov_numvfs.
	writeSysfsSymlink(t, root, "0000:17:00.1", "physfn", "../0000:17:00.0")
	mustMkdirAll(t, filepath.Join(root, "0000:17:00.0"))
	writeSysfsAttr(t, root, "0000:38:00.0", "sriov_numvfs", "0\n")

	getDevices = func(context.Context) ([]rblndevice.Device, error) {
		return []rblndevice.Device{
			vfDeviceForTest("rbln0-0", "0000:17:00.1"),
			vfDeviceForTest("rbln1", "0000:38:00.0"),
		}, nil
	}

	groups := discoverDeviceGroups(context.Background(), true)
	if len(groups) != 1 {
		t.Fatalf("expected only the healthy PF group, got %v", groupNames(groups))
	}
	generic, ok := groups[consts.GenericResourceName]
	if !ok {
		t.Fatalf("missing generic group, got %v", groupNames(groups))
	}
	if _, ok := generic.Devices["rbln0-0"]; ok {
		t.Fatalf("device with indeterminate SR-IOV state must not be advertised")
	}
	if _, ok := generic.Devices["rbln1"]; !ok {
		t.Fatalf("healthy sibling PF must survive the anomaly skip")
	}
}

func groupNames(groups map[string]DeviceGroup) []string {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	return names
}
