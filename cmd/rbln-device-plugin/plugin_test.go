package main

import (
	"context"
	"path/filepath"
	"testing"

	rblndevice "github.com/rbln-sw/rblnlib-go/pkg/device"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
)

// The device is named "null" so the /dev/<name> device-node poll resolves to
// the always-present /dev/null (same trick as the allocate test in
// cdi_test.go).
func TestAllocateVFSkipsRSDGroup(t *testing.T) {
	t.Parallel()

	cdi, err := NewCDIHandler(t.TempDir())
	if err != nil {
		t.Fatalf("new CDI handler: %v", err)
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
	rsdCalled := false
	plugin.rsdGroupFn = func([]string) (string, error) {
		rsdCalled = true
		return "/dev/rsd0", nil
	}

	response, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{DevicesIds: []string{"null"}},
		},
	})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}

	if rsdCalled {
		t.Fatalf("rsdGroupFn must not be called for VF resources")
	}
	if len(response.ContainerResponses) != 1 {
		t.Fatalf("expected 1 container response, got %d", len(response.ContainerResponses))
	}
	containerResponse := response.ContainerResponses[0]
	if got := containerResponse.Annotations["cdi.k8s.io/rebellions.ai_npu"]; got != consts.CDIKind+"="+consts.BaseCDIDevice {
		t.Fatalf("unexpected runtime annotation value %q", got)
	}
	if len(containerResponse.Devices) != 1 {
		t.Fatalf("expected only the VF device spec, got %d specs: %+v", len(containerResponse.Devices), containerResponse.Devices)
	}
	if got := containerResponse.Devices[0]; got.ContainerPath != "/dev/null" || got.HostPath != "/dev/null" || got.Permissions != "rw" {
		t.Fatalf("unexpected VF device spec: %+v", got)
	}
	for _, spec := range containerResponse.Devices {
		if spec.ContainerPath == "/dev/rsd0" {
			t.Fatalf("VF allocation must not expose /dev/rsd0")
		}
	}
}
