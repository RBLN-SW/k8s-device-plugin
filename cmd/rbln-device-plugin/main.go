package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/logging"
)

var version = "dev"

type Flags struct {
	cdiRoot                 string
	kubeletDevicePluginPath string
	healthcheckPort         int
	useGenericResourceName  bool
	deviceScanInterval      time.Duration
	otlpEndpoint            string
}

type Config struct {
	flags *Flags
	// Reported in the startup record so the stream states its own gate.
	logging logging.Settings
}

func (c Config) KubeletSocketPath() string {
	return filepath.Join(c.flags.kubeletDevicePluginPath, filepath.Base(pluginapi.KubeletSocket))
}

func main() {
	logSettings := logging.SetupFromEnv()
	bridgeGRPCLogs()
	if err := newApp(logSettings).Run(os.Args); err != nil {
		slog.Error("Command execution failed", "err", err)
		os.Exit(1)
	}
}

// Drop cli's "-v" alias for --version: klog's "-v 2" would otherwise parse as
// "print the version" and exit 0, so a DaemonSet with wrong args would look
// like it succeeded. Undefined, "-v" is a usage error instead. This must be an
// init: cli reads the global while building every App.
func init() {
	cli.VersionFlag = &cli.BoolFlag{Name: "version", Usage: "print the version"}
}

func newApp(logSettings logging.Settings) *cli.App {
	flags := &Flags{}

	cliFlags := []cli.Flag{
		&cli.StringFlag{
			Name:        "cdi-root",
			Usage:       "Absolute path to the directory where CDI files will be generated.",
			Value:       "/var/run/cdi",
			Destination: &flags.cdiRoot,
			EnvVars:     []string{"CDI_ROOT"},
		},
		&cli.StringFlag{
			Name:        "kubelet-device-plugin-path",
			Usage:       "Absolute path to the kubelet device-plugin directory.",
			Value:       pluginapi.DevicePluginPath,
			Destination: &flags.kubeletDevicePluginPath,
			EnvVars:     []string{"KUBELET_DEVICE_PLUGIN_PATH"},
		},
		&cli.IntFlag{
			Name:        "healthcheck-port",
			Usage:       "Port to start a gRPC healthcheck service. Use a negative value to disable it.",
			Value:       51515,
			Destination: &flags.healthcheckPort,
			EnvVars:     []string{"HEALTHCHECK_PORT"},
		},
		&cli.BoolFlag{
			Name:        "use-generic-resource-name",
			Usage:       "Expose devices as rebellions.ai/npu instead of legacy rebellions.ai/ATOM or rebellions.ai/REBEL resources.",
			Destination: &flags.useGenericResourceName,
			EnvVars:     []string{"USE_GENERIC_RESOURCE_NAME"},
		},
		&cli.DurationFlag{
			Name:        "device-scan-interval",
			Usage:       "Polling interval used to refresh the device inventory.",
			Value:       time.Minute,
			Destination: &flags.deviceScanInterval,
			EnvVars:     []string{"DEVICE_SCAN_INTERVAL"},
		},
		&cli.StringFlag{
			Name:        "otlp-endpoint",
			Usage:       "OTLP gRPC endpoint (e.g. host:port) to export allocation traces to. Leave empty to disable tracing.",
			Destination: &flags.otlpEndpoint,
			EnvVars:     []string{"OTEL_EXPORTER_OTLP_ENDPOINT"},
		},
	}

	app := &cli.App{
		Name:    "rbln-device-plugin",
		Version: version,
		Usage:   "rbln-device-plugin exposes Rebellions NPUs through the Kubernetes device plugin API.",
		// stdout is the log stream, so usage output goes to stderr: on a flag
		// error cli prints "Incorrect Usage" plus the whole help text through
		// Writer, which on stdout is a dozen unparseable lines interleaved with
		// the records a collector is reading.
		Writer:          os.Stderr,
		ErrWriter:       os.Stderr,
		ArgsUsage:       " ",
		HideHelpCommand: true,
		Flags:           cliFlags,
		Before: func(c *cli.Context) error {
			if c.Args().Len() > 0 {
				return fmt.Errorf("arguments not supported: %v", c.Args().Slice())
			}
			return nil
		},
		Action: func(c *cli.Context) error {
			return Run(c.Context, &Config{flags: flags, logging: logSettings})
		},
	}

	return app
}

func Run(ctx context.Context, config *Config) error {
	// The first record of the stream: which build is running, with which
	// configuration. Everything after it is interpreted against this line.
	slog.Info("Starting rbln-device-plugin",
		"version", version,
		"logLevel", config.logging.Level,
		"logFormat", config.logging.Format,
		"cdiRoot", config.flags.cdiRoot,
		"kubeletDevicePluginPath", config.flags.kubeletDevicePluginPath,
		"healthcheckPort", config.flags.healthcheckPort,
		"useGenericResourceName", config.flags.useGenericResourceName,
		"deviceScanInterval", config.flags.deviceScanInterval.String(),
	)

	if err := os.MkdirAll(config.flags.kubeletDevicePluginPath, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(config.flags.cdiRoot, 0o755); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchShutdownSignals(ctx, cancel)

	shutdownTracing, err := initTracing(ctx, config.flags.otlpEndpoint, version)
	if err != nil {
		return err
	}
	defer func() {
		// ctx is already canceled once we get here, so flush on a fresh,
		// bounded context to give buffered spans a chance to reach the backend.
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(flushCtx); err != nil {
			slog.Warn("Failed to flush traces on shutdown", "err", err)
		}
	}()

	manager, err := NewManager(config)
	if err != nil {
		return err
	}
	defer manager.Stop()

	if err := manager.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}

// watchShutdownSignals names the signal that ended the process, because
// "why did this pod restart" reads differently for a SIGTERM (kubelet draining
// or rolling the DaemonSet) than for a SIGQUIT, and nothing else records the
// distinction. A ctx cancelled elsewhere is a shutdown but not a signal, so it
// deliberately logs nothing here.
func watchShutdownSignals(ctx context.Context, cancel context.CancelFunc) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		defer signal.Stop(signals)
		select {
		case sig := <-signals:
			slog.Info("Shutdown signal received", "signal", sig.String())
			cancel()
		case <-ctx.Done():
		}
	}()
}
