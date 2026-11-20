package main

import (
	"encoding/json"
	"google.golang.org/protobuf/proto"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"os"
	"path/filepath"
	"testing"
)

func writeAllocation(t *testing.T, p *ResourcePlugin, ids []string, resp *pluginapi.ContainerAllocateResponse) {
	t.Helper()
	b, err := proto.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"ResourceName": p.resourceName, "DeviceIDs": map[string][]string{"0": ids}, "AllocResp": b}
	cp, err := json.Marshal(map[string]any{"Data": map[string]any{"PodDeviceEntries": []any{entry}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(p.kubeletSocket), "kubelet_internal_checkpoint"), cp, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCachedAllocationBindsPCIIdentity(t *testing.T) {
	p := &ResourcePlugin{resourceName: "rebellions.ai/npu", kubeletSocket: filepath.Join(t.TempDir(), "kubelet.sock")}
	ids := []string{"a", "b"}
	devices := []NPUDevice{testDevice("null", "a", "0000:01:00.0", "0"), testDevice("zero", "b", "0000:03:00.0", "0")}
	alias := "/dev/.rbln-k8s-prestart-rsd/0000:01:00.0"
	response := &pluginapi.ContainerAllocateResponse{Devices: []*pluginapi.DeviceSpec{{ContainerPath: "/dev/rsd0", HostPath: alias}}, Annotations: map[string]string{p.identityKey(): pciIdentity(ids, devices)}}
	writeAllocation(t, p, ids, response)
	devices[1].Info.PCIBusID = "0000:04:00.0"
	if _, err := p.cachedRSD(ids, devices, alias); err == nil {
		t.Fatal("accepted changed non-anchor device")
	}
	response.Annotations = nil
	response.Devices[0].HostPath = "/dev/rsd2"
	writeAllocation(t, p, ids, response)
	if path, err := p.cachedRSD(ids, devices, alias); err != nil || path != "/dev/rsd2" {
		t.Fatalf("legacy path = %q, %v", path, err)
	}
}
