package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"k8s.io/klog/v2"
)

const tracerName = "github.com/RBLN-SW/k8s-device-plugin"

// tracer is fetched from the global TracerProvider. The global provider is a
// no-op until initTracing installs a real one, and any tracer handed out before
// then transparently starts delegating once the real provider is set, so it is
// safe to capture this at package-initialization time.
var tracer = otel.Tracer(tracerName)

const (
	attrResourceName = "rbln.resource_name"
	attrDeviceIDs    = "rbln.device.ids"
	attrDeviceCount  = "rbln.device.count"
	attrBusIDs       = "rbln.device.bus_ids"
	attrRsdHostPath  = "rbln.rsd.host_path"
)

func initTracing(ctx context.Context, endpoint, serviceVersion string) (func(context.Context) error, error) {
	if endpoint == "" {
		klog.InfoS("OTLP endpoint not configured; distributed tracing disabled")
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := newOTLPExporter(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}

	res, err := newTracingResource(serviceVersion)
	if err != nil {
		return nil, fmt.Errorf("build tracing resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	klog.InfoS("distributed tracing enabled", "endpoint", endpoint)
	return provider.Shutdown, nil
}

func newOTLPExporter(ctx context.Context, endpoint string) (sdktrace.SpanExporter, error) {
	var opts []otlptracegrpc.Option
	if strings.Contains(endpoint, "://") {
		// WithEndpointURL swallows a parse failure (it logs and silently falls
		// back to the default localhost endpoint), so validate up front and
		// surface a malformed endpoint as an explicit error instead.
		if _, err := url.Parse(endpoint); err != nil {
			return nil, fmt.Errorf("parse OTLP endpoint URL %q: %w", endpoint, err)
		}
		opts = append(opts, otlptracegrpc.WithEndpointURL(endpoint))
	} else {
		opts = append(opts, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
	}
	return otlptracegrpc.New(ctx, opts...)
}

func newTracingResource(serviceVersion string) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", "rbln-device-plugin"),
		attribute.String("service.version", serviceVersion),
	}
	if node := os.Getenv("NODE_NAME"); node != "" {
		attrs = append(attrs, attribute.String("k8s.node.name", node))
	}

	return resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
}
