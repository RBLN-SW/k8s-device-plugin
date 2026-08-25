package main

import (
	"context"
	"log/slog"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
)

// The OTEL SDK reports asynchronously — a failed export through otel.Handle, a
// malformed OTEL_EXPORTER_OTLP_* value through its internal logger — and both
// defaults write plain text to stderr with the standard log package. Left alone
// an unreachable collector is invisible to anything reading this process's
// structured stream: the same gap bridgeGRPCLogs closes for gRPC.
//
// Nothing the SDK reports rises above warn. Tracing is best-effort by
// construction, so no failure here makes a device or an allocation unusable,
// and error is reserved for exactly that.

// bridgeOTELLogs must run before initTracing installs a provider, so no SDK
// record can reach stderr ahead of the bridge.
func bridgeOTELLogs() {
	otel.SetErrorHandler(otelErrorHandler{})
	otel.SetLogger(logr.New(otelLogSink{}))
}

type otelErrorHandler struct{}

// Handle repeats for as long as the outage lasts, because the exporter retries
// on its own schedule and a collector that is still down is still news.
func (otelErrorHandler) Handle(err error) {
	otelLog(slog.LevelWarn, "OpenTelemetry SDK error", "err", err)
}

// otelLogSink adapts the SDK's logr logger. logr's own slog adapter maps V(n)
// to slog level -n, which would print levels like "debug+3" and break the
// error|warn|info|debug vocabulary the stream is read with, so the severities
// are pinned here instead.
type otelLogSink struct {
	attrs []any
}

func (otelLogSink) Init(logr.RuntimeInfo) {}

// Enabled gates the SDK's verbose logging on this process's debug level, since
// that is where its informational records land.
func (otelLogSink) Enabled(int) bool {
	return slog.Default().Enabled(context.Background(), slog.LevelDebug)
}

// Info covers every V-level the SDK uses: it is per-export bookkeeping, so it
// maps to debug regardless of how verbose the SDK considers it.
func (s otelLogSink) Info(_ int, msg string, kv ...any) {
	otelLog(slog.LevelDebug, msg, s.args(kv)...)
}

func (s otelLogSink) Error(err error, msg string, kv ...any) {
	otelLog(slog.LevelWarn, msg, append(s.args(kv), "err", err)...)
}

func (s otelLogSink) WithValues(kv ...any) logr.LogSink {
	return otelLogSink{attrs: s.args(kv)}
}

func (s otelLogSink) WithName(string) logr.LogSink { return s }

func (s otelLogSink) args(kv []any) []any {
	args := make([]any, 0, len(s.attrs)+len(kv)+4)
	args = append(args, s.attrs...)
	return append(args, kv...)
}

// otelLog tags every record with component=otel because, unlike this binary's
// own records, the message text is library-formatted rather than a stable
// constant.
func otelLog(level slog.Level, msg string, args ...any) {
	slog.Log(context.Background(), level, msg, append(args, "component", "otel")...)
}
