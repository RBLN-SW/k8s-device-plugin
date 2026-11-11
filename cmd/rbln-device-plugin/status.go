package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// Device lifecycle values mirror rbln_dev_status and rblnmlDeviceStatus_t.
const (
	deviceStatusReady   = 0
	deviceStatusBusy    = 1
	deviceStatusProbing = 2
	deviceStatusInit    = 3
	deviceStatusFault   = 4
	deviceStatusFinish  = 5
)

// deviceStatusUnreadable stands in for the status name when sysfs cannot be
// read, so an unreadable device is still described by one stable token.
const deviceStatusUnreadable = "UNREADABLE"

var rebellionsSysfsClassPath = "/sys/class/rebellions"

// TODO: Replace direct sysfs reads with go-rbln-ml's status API.
func readDeviceStatus(deviceName string) (int, error) {
	statusPath := filepath.Join(rebellionsSysfsClassPath, deviceName, "status")
	raw, err := os.ReadFile(statusPath)
	if err != nil {
		return 0, err
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("parse status file %s: %w", statusPath, err)
	}
	return status, nil
}

// deviceStatusName names raw kernel lifecycle states for diagnostics.
func deviceStatusName(status int) string {
	switch status {
	case deviceStatusReady:
		return "READY"
	case deviceStatusBusy:
		return "BUSY"
	case deviceStatusProbing:
		return "PROBING"
	case deviceStatusInit:
		return "INIT"
	case deviceStatusFault:
		return "FAULT"
	case deviceStatusFinish:
		return "FINISH"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", status)
	}
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

	if status == deviceStatusReady {
		return pluginapi.Healthy, deviceStatusName(status)
	}

	return pluginapi.Unhealthy, deviceStatusName(status)
}
