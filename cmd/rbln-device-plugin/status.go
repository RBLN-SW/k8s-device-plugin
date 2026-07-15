package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	deviceStatusReady  = 0
	deviceStatusBusy   = 1
	deviceStatusInit   = 2
	deviceStatusFault  = 3
	deviceStatusFinish = 4
)

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

func healthForDevice(deviceName string) string {
	status, err := readDeviceStatus(deviceName)
	if err != nil {
		klog.ErrorS(err, "failed to read device status; assuming healthy", "device", deviceName)
		return pluginapi.Healthy
	}

	if status == deviceStatusReady {
		return pluginapi.Healthy
	}

	klog.InfoS("device is not serviceable; reporting unhealthy",
		"device", deviceName,
		"status", status,
		"statusName", deviceStatusName(status),
	)
	return pluginapi.Unhealthy
}
