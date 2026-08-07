package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	rblndevice "github.com/rbln-sw/rblnlib-go/pkg/device"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
)

// The device is named "null" so the /dev/<name> device-node poll resolves to
// the always-present /dev/null (same trick as the allocate test in
// cdi_test.go).
func TestAllocateVFCreatesRSDGroup(t *testing.T) {
	t.Parallel()

	cdi, err := NewCDIHandler(t.TempDir())
	if err != nil {
		t.Fatalf("new CDI handler: %v", err)
	}
	rsdPath := filepath.Join(t.TempDir(), "rsd0")
	if err := os.WriteFile(rsdPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write rsd device placeholder: %v", err)
	}

	plugin := NewResourcePlugin(
		"rebellions.ai/npu-vf4",
		filepath.Join(t.TempDir(), "rbln.sock"),
		filepath.Join(t.TempDir(), "kubelet.sock"),
		cdi,
		map[string]NPUDevice{
			"null": {
				Info: rblndevice.Device{
					Name:        "null",
					ProductName: "RBLN-CR03",
					PCIBusID:    "0000:17:00.1",
				},
				ParentPFBusID: "0000:17:00.0",
			},
		},
	)
	var rsdBusIDs []string
	plugin.rsdGroupFn = func(busIDs []string) (string, error) {
		rsdBusIDs = busIDs
		return rsdPath, nil
	}

	response, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{DevicesIds: []string{"null"}},
		},
	})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}

	if len(rsdBusIDs) != 1 || rsdBusIDs[0] != "0000:17:00.1" {
		t.Fatalf("expected rsdGroupFn to be called with the VF bus ID, got %v", rsdBusIDs)
	}
	if len(response.ContainerResponses) != 1 {
		t.Fatalf("expected 1 container response, got %d", len(response.ContainerResponses))
	}
	containerResponse := response.ContainerResponses[0]
	if got := containerResponse.Annotations["cdi.k8s.io/rebellions.ai_npu"]; got != consts.CDIKind+"="+consts.BaseCDIDevice {
		t.Fatalf("unexpected runtime annotation value %q", got)
	}
	if len(containerResponse.Devices) != 2 {
		t.Fatalf("expected rsd and VF device specs, got %d specs: %+v", len(containerResponse.Devices), containerResponse.Devices)
	}
	if got := containerResponse.Devices[0]; got.ContainerPath != "/dev/rsd0" || got.HostPath != rsdPath || got.Permissions != "rw" {
		t.Fatalf("unexpected rsd device spec: %+v", got)
	}
	if got := containerResponse.Devices[1]; got.ContainerPath != "/dev/null" || got.HostPath != "/dev/null" || got.Permissions != "rw" {
		t.Fatalf("unexpected VF device spec: %+v", got)
	}
}
