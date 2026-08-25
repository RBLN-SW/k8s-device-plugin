package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"

	rblndevice "github.com/rbln-sw/rblnlib-go/pkg/device"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
)

var getDevices = rblndevice.GetDevices

// unsupportedProductsLogged keeps the unsupported-product error to one record
// per device: a product this build does not know about stays unknown for the
// process's lifetime, and discovery re-runs every scan interval.
var unsupportedProductsLogged sync.Map

type NPUDevice struct {
	Info   rblndevice.Device
	Health string
	// StatusName is the underlying device status ("READY", "FAULT", ...), kept
	// alongside the coarse kubelet health so logs can say why a device is down.
	StatusName string
}

type DeviceGroup struct {
	ResourceName string
	Devices      map[string]NPUDevice
}

func discoverDeviceGroups(ctx context.Context, useGenericResourceName bool) map[string]DeviceGroup {
	groups := make(map[string]DeviceGroup)

	devices, err := getDevices(ctx)
	if err != nil {
		slog.Error("Device discovery failed; reporting zero devices until it recovers", "err", err)
		return groups
	}

	for _, device := range devices {
		resourceName, err := resourceNameForProduct(device.ProductName, useGenericResourceName)
		if err != nil {
			if _, seen := unsupportedProductsLogged.LoadOrStore(device.Name+"/"+device.ProductName, struct{}{}); !seen {
				slog.Error("Skipping device with unsupported product; it stays invisible to Kubernetes",
					"err", err,
					"device", device.Name,
					"product", device.ProductName,
				)
			}
			continue
		}
		group, ok := groups[resourceName]
		if !ok {
			group = DeviceGroup{
				ResourceName: resourceName,
				Devices:      make(map[string]NPUDevice),
			}
		}
		health, statusName := healthForDevice(device.Name)
		group.Devices[device.Name] = NPUDevice{
			Info:       device,
			Health:     health,
			StatusName: statusName,
		}
		groups[resourceName] = group
	}

	return groups
}

type deviceStateChange struct {
	Device         NPUDevice
	PreviousHealth string
	PreviousStatus string
}

// All slices are sorted by device ID so log order is stable across scans.
type deviceInventoryDelta struct {
	Added   []NPUDevice
	Removed []string
	Changed []deviceStateChange
}

func (d deviceInventoryDelta) isEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// diffDeviceInventories exists so the scan loop can log state *transitions*
// instead of restating every device's state on every scan.
func diffDeviceInventories(previous, current map[string]NPUDevice) deviceInventoryDelta {
	delta := deviceInventoryDelta{}

	for _, id := range sortedDeviceIDs(current) {
		device := current[id]
		before, existed := previous[id]
		switch {
		case !existed:
			delta.Added = append(delta.Added, device)
		case before.Health != device.Health || before.StatusName != device.StatusName:
			delta.Changed = append(delta.Changed, deviceStateChange{
				Device:         device,
				PreviousHealth: before.Health,
				PreviousStatus: before.StatusName,
			})
		}
	}

	for _, id := range sortedDeviceIDs(previous) {
		if _, stillPresent := current[id]; !stillPresent {
			delta.Removed = append(delta.Removed, id)
		}
	}

	return delta
}

// deviceLogAttrs is shared by every record about a device so registration and
// inventory-change records answer the same queries, instead of each carrying a
// different subset of the identity keys.
func deviceLogAttrs(device NPUDevice) []any {
	return []any{
		"device", device.Info.Name,
		"product", device.Info.ProductName,
		"pciDeviceID", device.Info.PCIDeviceID,
		"pciBusID", device.Info.PCIBusID,
		"numa", device.Info.PCINumaNode,
		"health", device.Health,
		"status", device.StatusName,
	}
}

// levelForHealth keeps "this device is unusable now" above the info gate
// wherever device state is reported: a device already faulted at startup is as
// actionable as one that faults later, so warn-based alerting must catch both.
func levelForHealth(health string) slog.Level {
	if health == pluginapi.Unhealthy {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

func healthCounts(devices map[string]NPUDevice) (healthy, unhealthy int) {
	for _, device := range devices {
		if device.Health == pluginapi.Unhealthy {
			unhealthy++
			continue
		}
		healthy++
	}
	return healthy, unhealthy
}

func resourceNameForProduct(productName string, useGenericResourceName bool) (string, error) {
	if useGenericResourceName {
		return consts.GenericResourceName, nil
	}

	switch {
	case strings.HasPrefix(productName, "RBLN-CR"):
		return consts.RebelResourceName, nil
	case strings.HasPrefix(productName, "RBLN-CA"):
		return consts.AtomResourceName, nil
	default:
		return "", fmt.Errorf("unsupported ProductName %q: expected prefix RBLN-CR or RBLN-CA", productName)
	}
}

func toPluginDevice(device NPUDevice) *pluginapi.Device {
	health := device.Health
	if health == "" {
		health = pluginapi.Healthy
	}
	return &pluginapi.Device{
		ID:       device.Info.Name,
		Health:   health,
		Topology: topologyForDevice(device.Info.PCINumaNode),
	}
}

func topologyForDevice(numaNode string) *pluginapi.TopologyInfo {
	if numaNode == "" {
		return nil
	}
	id, err := strconv.ParseInt(numaNode, 10, 64)
	if err != nil {
		return nil
	}
	return &pluginapi.TopologyInfo{
		Nodes: []*pluginapi.NUMANode{{ID: id}},
	}
}

func clonePluginDevices(devices map[string]NPUDevice) []*pluginapi.Device {
	ids := sortedDeviceIDs(devices)
	pluginDevices := make([]*pluginapi.Device, 0, len(ids))
	for _, id := range ids {
		pluginDevices = append(pluginDevices, toPluginDevice(devices[id]))
	}
	return pluginDevices
}

func sortedDeviceIDs(devices map[string]NPUDevice) []string {
	ids := make([]string, 0, len(devices))
	for id := range devices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func resourceSlug(resourceName string) string {
	slug := strings.ToLower(resourceName)
	slug = strings.ReplaceAll(slug, "/", "-")
	slug = strings.ReplaceAll(slug, ".", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	return slug
}
