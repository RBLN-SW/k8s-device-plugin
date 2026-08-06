package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
)

// TestProbeSRIOV is not parallel: it mutates the shared package-level
// sriovPCISysfsDevicesPath seam.
func TestProbeSRIOV(t *testing.T) {
	originalPath := sriovPCISysfsDevicesPath
	t.Cleanup(func() {
		sriovPCISysfsDevicesPath = originalPath
	})

	root := t.TempDir()
	sriovPCISysfsDevicesPath = root

	// PF hosting 4 VFs.
	writeSysfsAttr(t, root, "0000:17:00.0", "sriov_numvfs", "4\n")
	// VF of that PF.
	writeSysfsSymlink(t, root, "0000:17:00.1", "physfn", "../0000:17:00.0")
	// Non-partitioned PF with SR-IOV capability but zero VFs.
	writeSysfsAttr(t, root, "0000:38:00.0", "sriov_numvfs", "0\n")
	// Device without any SR-IOV attributes.
	mustMkdirAll(t, filepath.Join(root, "0000:59:00.0"))
	// VF whose parent PF lacks a readable sriov_numvfs.
	writeSysfsSymlink(t, root, "0000:7a:00.1", "physfn", "../0000:7a:00.0")
	mustMkdirAll(t, filepath.Join(root, "0000:7a:00.0"))
	// PF with a garbled sriov_numvfs.
	writeSysfsAttr(t, root, "0000:9b:00.0", "sriov_numvfs", "not-a-number\n")

	tests := []struct {
		name     string
		pciBusID string
		expected sriovInfo
		wantErr  bool
	}{
		{
			name:     "vf with hosting parent",
			pciBusID: "0000:17:00.1",
			expected: sriovInfo{class: sriovClassVF, parentPFBusID: "0000:17:00.0", numVFs: 4},
		},
		{
			name:     "vf hosting pf",
			pciBusID: "0000:17:00.0",
			expected: sriovInfo{class: sriovClassHostingPF, numVFs: 4},
		},
		{
			name:     "pf with zero vfs",
			pciBusID: "0000:38:00.0",
			expected: sriovInfo{class: sriovClassNone},
		},
		{
			name:     "device without sriov attributes",
			pciBusID: "0000:59:00.0",
			expected: sriovInfo{class: sriovClassNone},
		},
		{
			name:     "device missing from sysfs",
			pciBusID: "0000:ff:00.0",
			expected: sriovInfo{class: sriovClassNone},
		},
		{
			name:     "empty bus id",
			pciBusID: "",
			expected: sriovInfo{class: sriovClassNone},
		},
		{
			name:     "vf with unreadable parent numvfs",
			pciBusID: "0000:7a:00.1",
			wantErr:  true,
		},
		{
			name:     "pf with unparseable numvfs",
			pciBusID: "0000:9b:00.0",
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := probeSRIOV(tc.pciBusID)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %+v", tc.pciBusID, actual)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if actual != tc.expected {
				t.Fatalf("expected %+v, got %+v", tc.expected, actual)
			}
		})
	}
}

func TestVFResourceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		numVFs   int
		expected string
		wantErr  bool
	}{
		{name: "vf-1 mode", numVFs: 1, expected: "rebellions.ai/npu-vf1"},
		{name: "vf-4 mode", numVFs: 4, expected: "rebellions.ai/npu-vf4"},
		{name: "vf-2 is unsupported", numVFs: 2, wantErr: true},
		{name: "zero is unsupported", numVFs: 0, wantErr: true},
		{name: "vf-8 is unsupported", numVFs: 8, wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			actual, err := vfResourceName(tc.numVFs)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %d VFs", tc.numVFs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if actual != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, actual)
			}
		})
	}
}

func TestIsVFResourceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		resourceName string
		expected     bool
	}{
		{resourceName: "rebellions.ai/npu-vf1", expected: true},
		{resourceName: "rebellions.ai/npu-vf4", expected: true},
		{resourceName: consts.GenericResourceName, expected: false},
		{resourceName: consts.AtomResourceName, expected: false},
		{resourceName: consts.RebelResourceName, expected: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.resourceName, func(t *testing.T) {
			t.Parallel()

			if actual := isVFResourceName(tc.resourceName); actual != tc.expected {
				t.Fatalf("isVFResourceName(%q) = %v, expected %v", tc.resourceName, actual, tc.expected)
			}
		})
	}
}

func TestResourceSlugForVFResource(t *testing.T) {
	t.Parallel()

	if slug := resourceSlug("rebellions.ai/npu-vf4"); slug != "rebellions-ai-npu-vf4" {
		t.Fatalf("unexpected slug %q for VF resource", slug)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeSysfsAttr(t *testing.T, root, device, attr, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Join(root, device))
	if err := os.WriteFile(filepath.Join(root, device, attr), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s/%s: %v", device, attr, err)
	}
}

func writeSysfsSymlink(t *testing.T, root, device, name, target string) {
	t.Helper()
	mustMkdirAll(t, filepath.Join(root, device))
	if err := os.Symlink(target, filepath.Join(root, device, name)); err != nil {
		t.Fatalf("symlink %s/%s: %v", device, name, err)
	}
}
