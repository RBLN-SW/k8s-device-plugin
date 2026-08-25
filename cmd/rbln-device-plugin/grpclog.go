package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/grpc/grpclog"
)

// grpcLogger routes gRPC's internal logger into slog. Without it gRPC writes
// plain-text records straight to stderr, so a process that otherwise emits one
// structured stream on stdout would still hide transport failures — exactly the
// records an operator needs when kubelet registration or a plugin socket breaks.
//
// gRPC's own info chatter is per-connection bookkeeping, so it maps to debug;
// only warnings and errors reach the default gate. Every record carries
// component=grpc because, unlike this binary's own records, the message text is
// library-formatted rather than a stable constant.
type grpcLogger struct{}

// bridgeGRPCLogs must run before any gRPC call: grpclog.SetLoggerV2 is not
// safe to call once gRPC is in use.
func bridgeGRPCLogs() {
	grpclog.SetLoggerV2(grpcLogger{})
}

func (grpcLogger) log(level slog.Level, msg string) {
	slog.Log(context.Background(), level, strings.TrimRight(msg, "\n"), "component", "grpc")
}

func (l grpcLogger) Info(args ...any) { l.log(slog.LevelDebug, fmt.Sprint(args...)) }
func (l grpcLogger) Infoln(args ...any) {
	l.log(slog.LevelDebug, fmt.Sprintln(args...))
}
func (l grpcLogger) Infof(format string, args ...any) {
	l.log(slog.LevelDebug, fmt.Sprintf(format, args...))
}

func (l grpcLogger) Warning(args ...any) { l.log(slog.LevelWarn, fmt.Sprint(args...)) }
func (l grpcLogger) Warningln(args ...any) {
	l.log(slog.LevelWarn, fmt.Sprintln(args...))
}
func (l grpcLogger) Warningf(format string, args ...any) {
	l.log(slog.LevelWarn, fmt.Sprintf(format, args...))
}

func (l grpcLogger) Error(args ...any) { l.log(slog.LevelError, fmt.Sprint(args...)) }
func (l grpcLogger) Errorln(args ...any) {
	l.log(slog.LevelError, fmt.Sprintln(args...))
}
func (l grpcLogger) Errorf(format string, args ...any) {
	l.log(slog.LevelError, fmt.Sprintf(format, args...))
}

// gRPC exits with os.Exit(1) after any Fatal call, so these only need to make
// the reason visible.
func (l grpcLogger) Fatal(args ...any) { l.log(slog.LevelError, fmt.Sprint(args...)) }
func (l grpcLogger) Fatalln(args ...any) {
	l.log(slog.LevelError, fmt.Sprintln(args...))
}
func (l grpcLogger) Fatalf(format string, args ...any) {
	l.log(slog.LevelError, fmt.Sprintf(format, args...))
}

// V gates gRPC's verbose logging on this process's debug level, since that is
// where its info records land.
func (grpcLogger) V(int) bool {
	return slog.Default().Enabled(context.Background(), slog.LevelDebug)
}
