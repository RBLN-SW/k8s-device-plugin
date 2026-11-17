package main

import (
	"context"
	host "github.com/rbln-sw/rblnlib-go/pkg/host/v1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"log/slog"
	"path/filepath"
)

const deviceStatusUnreadable = "UNREADABLE"

var rebellionsSysfsClassPath = "/sys/class/rebellions"

func readDeviceStatus(name string) (system.StateObservation, error) {
	c, err := host.New(host.Options{RebellionsClassRoot: rebellionsSysfsClassPath, AccelClassRoot: filepath.Join(filepath.Dir(rebellionsSysfsClassPath), "accel")})
	if err != nil {
		return system.StateObservation{}, err
	}
	return c.ReadNamedState(context.Background(), name)
}

// healthForDevice deliberately does not log its outcome: it runs for every
// device on every scan, so logging here would repeat the same line every scan
// interval for as long as a device stays unhealthy. The manager reports health
// as state *changes* instead (see ResourcePlugin.UpdateDevices).
//
// An unreadable status preserves a known Unhealthy state. Without an unresolved
// fault, retain the compatibility default for drivers/VFs with no status file.
// Only an observed READY clears an Unhealthy state. The caller serializes the
// read and history update with ResourcePlugin.mu.
func healthForDevice(deviceName, previousHealth string) (health, statusName string) {
	status, err := readDeviceStatus(deviceName)
	if err != nil {
		health = pluginapi.Healthy
		if previousHealth == pluginapi.Unhealthy {
			health = pluginapi.Unhealthy
		}
		slog.Debug("Failed to read device status; retaining health", "err", err, "device", deviceName, "health", health)
		return health, deviceStatusUnreadable
	}

	if status.State == system.StateReady {
		return pluginapi.Healthy, status.String()
	}

	return pluginapi.Unhealthy, status.String()
}
