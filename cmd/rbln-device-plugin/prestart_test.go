package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestOverlappingRequestsPublishOnlyAtContainerStart(t *testing.T) {
	base := t.TempDir()
	groups := map[int]string{1: filepath.Join(base, "rsd1"), 2: filepath.Join(base, "rsd2")}
	for _, group := range groups {
		if err := os.WriteFile(group, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cdi, err := NewCDIHandler(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newPlugin := func() *ResourcePlugin {
		p := NewResourcePlugin("rebellions.ai/npu", "", "", cdi, map[string]NPUDevice{
			"a": testDevice("null", "a", "0000:01:00.0", "0"),
			"b": testDevice("zero", "b", "0000:02:00.0", "0"),
		})
		p.rsdAliasDir = filepath.Join(base, "aliases")
		p.resolveNodes = testNativeNodes
		p.rsdGroupFn = func(_ context.Context, buses []string) (string, error) { return groups[len(buses)], nil }
		return p
	}
	p := newPlugin()
	opts, err := p.GetDevicePluginOptions(t.Context(), &pluginapi.Empty{})
	if err != nil || !opts.PreStartRequired {
		t.Fatalf("prestart required: %+v %v", opts, err)
	}
	allocate := func(ids ...string) string {
		resp, err := p.Allocate(t.Context(), &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: ids}}})
		if err != nil {
			t.Fatal(err)
		}
		return resp.ContainerResponses[0].Devices[0].HostPath
	}
	phase := func(p *ResourcePlugin, alias string, ids ...string) {
		t.Helper()
		if _, err := p.PreStartContainer(t.Context(), &pluginapi.PreStartContainerRequest{DevicesIds: ids}); err != nil {
			t.Fatal(err)
		}
		got, err := os.Stat(alias)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.Stat(groups[len(ids)])
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(got, want) {
			t.Fatalf("%s does not identify %s", alias, groups[len(ids)])
		}
	}
	initAlias := allocate("b", "a")
	appAlias := allocate("a")
	if _, err := os.Stat(initAlias); !os.IsNotExist(err) {
		t.Fatalf("admission published %s: %v", initAlias, err)
	}
	phase(p, initAlias, "a", "b")
	// Restart with fresh process state before starting the application. Here
	// both sets use the same anchor; the alias must atomically change target.
	phase(newPlugin(), appAlias, "a")
	phase(newPlugin(), appAlias, "a")
}

func TestPublishFailurePreservesExistingAlias(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(root, "aliases", "0000:01:00.0")
	first := filepath.Join(root, "rsd1")
	if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishRSDNode(first, alias); err != nil {
		t.Fatal(err)
	}
	if err := publishRSDNode(filepath.Join(root, "missing"), alias); err == nil {
		t.Fatal("unavailable device published")
	}
	if b, err := os.ReadFile(alias); err != nil || string(b) != "first" {
		t.Fatalf("previous alias changed: %q, %v", b, err)
	}
}
