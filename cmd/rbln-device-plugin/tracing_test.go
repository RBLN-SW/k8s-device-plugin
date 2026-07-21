package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
)

// TestAllocateEmitsSpans exercises the OTEL instrumentation on the Allocate
// path. It is a single non-parallel test because the package-level tracer binds
// its delegate to the first global TracerProvider installed, so all span
// assertions must share one provider.
func TestAllocateEmitsSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))

	t.Run("success records allocation attributes", func(t *testing.T) {
		plugin, rsdPath := newTracingTestPlugin(t)

		before := len(recorder.Ended())
		if _, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{
			ContainerRequests: []*pluginapi.ContainerAllocateRequest{
				{DevicesIds: []string{"rbln0"}},
			},
		}); err != nil {
			t.Fatalf("allocate: %v", err)
		}
		spans := recorder.Ended()[before:]

		parent := requireSpan(t, spans, "Allocate")
		child := requireSpan(t, spans, "allocateContainer")

		if parent.Status().Code == otelcodes.Error {
			t.Fatalf("expected Allocate span to succeed, got error status: %q", parent.Status().Description)
		}
		if child.Status().Code == otelcodes.Error {
			t.Fatalf("expected allocateContainer span to succeed, got error status: %q", child.Status().Description)
		}

		if child.Parent().SpanID() != parent.SpanContext().SpanID() {
			t.Fatalf("allocateContainer is not a child of Allocate")
		}
		if child.SpanContext().TraceID() != parent.SpanContext().TraceID() {
			t.Fatalf("child and parent spans are not in the same trace")
		}

		attrs := attributeMap(child)
		if got := attrs[attrResourceName].AsString(); got != plugin.resourceName {
			t.Fatalf("%s = %q, want %q", attrResourceName, got, plugin.resourceName)
		}
		if got := attrs[attrDeviceCount].AsInt64(); got != 1 {
			t.Fatalf("%s = %d, want 1", attrDeviceCount, got)
		}
		if got := attrs[attrDeviceIDs].AsStringSlice(); len(got) != 1 || got[0] != "rbln0" {
			t.Fatalf("%s = %v, want [rbln0]", attrDeviceIDs, got)
		}
		if got := attrs[attrBusIDs].AsStringSlice(); len(got) != 1 || got[0] != "0000:06:00.0" {
			t.Fatalf("%s = %v, want [0000:06:00.0]", attrBusIDs, got)
		}
		if got := attrs[attrRsdHostPath].AsString(); got != rsdPath {
			t.Fatalf("%s = %q, want %q", attrRsdHostPath, got, rsdPath)
		}
	})

	t.Run("failure marks spans as errored", func(t *testing.T) {
		plugin, _ := newTracingTestPlugin(t)

		before := len(recorder.Ended())
		if _, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{
			ContainerRequests: []*pluginapi.ContainerAllocateRequest{
				{DevicesIds: []string{"ghost"}},
			},
		}); err == nil {
			t.Fatalf("expected allocate to fail for an unmanaged device")
		}
		spans := recorder.Ended()[before:]

		parent := requireSpan(t, spans, "Allocate")
		child := requireSpan(t, spans, "allocateContainer")

		if child.Status().Code != otelcodes.Error {
			t.Fatalf("expected allocateContainer span error status, got %v", child.Status().Code)
		}
		if len(child.Events()) == 0 {
			t.Fatalf("expected an error event recorded on the allocateContainer span")
		}
		if parent.Status().Code != otelcodes.Error {
			t.Fatalf("expected Allocate span error status, got %v", parent.Status().Code)
		}
	})
}

func newTracingTestPlugin(t *testing.T) (*ResourcePlugin, string) {
	t.Helper()

	cdi, err := NewCDIHandler(t.TempDir())
	if err != nil {
		t.Fatalf("new CDI handler: %v", err)
	}
	if err := cdi.Initialize(); err != nil {
		t.Fatalf("initialize CDI handler: %v", err)
	}

	rsdPath := filepath.Join(t.TempDir(), "rsd0")
	if err := os.WriteFile(rsdPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write rsd device placeholder: %v", err)
	}

	plugin := NewResourcePlugin(
		consts.AtomResourceName,
		filepath.Join(t.TempDir(), "rbln.sock"),
		filepath.Join(t.TempDir(), "kubelet.sock"),
		cdi,
		map[string]NPUDevice{
			"rbln0": testDevice("null", "sid-a", "0000:06:00.0", "0"),
		},
	)
	plugin.rsdGroupFn = func([]string) (string, error) { return rsdPath, nil }

	return plugin, rsdPath
}

func requireSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	t.Fatalf("span %q not found among %d recorded spans", name, len(spans))
	return nil
}

func attributeMap(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	attrs := make(map[attribute.Key]attribute.Value)
	for _, kv := range span.Attributes() {
		attrs[kv.Key] = kv.Value
	}
	return attrs
}
