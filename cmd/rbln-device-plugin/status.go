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

const (
	deviceStatusReady  = 0
	deviceStatusBusy   = 1
	deviceStatusInit   = 2
	deviceStatusFault  = 3
	deviceStatusFinish = 4
)

// deviceStatusUnreadable stands in for the status name when sysfs cannot be
// read, so an unreadable device is still described by one stable token.
const deviceStatusUnreadable = "UNREADABLE"

var rebellionsSysfsClassPath = "/sys/class/rebellions"

// TODO: The device status is currently read directly from sysfs; switch to
// reading it through go-rbln-ml (or similar) once its status API is available.
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

// TODO: A future KMD version inserts a PROBING state at value 2, shifting
// INIT/FAULT/FINISH to 3/4/5 — update this mapping once that version ships.
func deviceStatusName(status int) string {
	switch status {
	case deviceStatusReady:
		return "READY"
	case deviceStatusBusy:
		return "BUSY"
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
// An unreadable status fails open (Healthy) so a transient sysfs error cannot
// drain the node's allocatable devices; the read error itself is only
// interesting while debugging.
func healthForDevice(deviceName string) (health, statusName string) {
	status, err := readDeviceStatus(deviceName)
	if err != nil {
		slog.Debug("Failed to read device status; assuming healthy", "err", err, "device", deviceName)
		return pluginapi.Healthy, deviceStatusUnreadable
	}

	if status == deviceStatusReady {
		return pluginapi.Healthy, deviceStatusName(status)
	}

	return pluginapi.Unhealthy, deviceStatusName(status)
}
