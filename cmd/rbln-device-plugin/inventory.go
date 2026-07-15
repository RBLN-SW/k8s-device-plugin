package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	rblndevice "github.com/rbln-sw/rblnlib-go/pkg/device"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
)

var getDevices = rblndevice.GetDevices

type NPUDevice struct {
	Info   rblndevice.Device
	Health string
}

type DeviceGroup struct {
	ResourceName string
	Devices      map[string]NPUDevice
}

func discoverDeviceGroups(ctx context.Context, useGenericResourceName bool) map[string]DeviceGroup {
	groups := make(map[string]DeviceGroup)

	devices, err := getDevices(ctx)
	if err != nil {
		klog.ErrorS(err, "device discovery failed; reporting zero devices until it recovers")
		return groups
	}

	for _, device := range devices {
		resourceName, err := resourceNameForProduct(device.ProductName, useGenericResourceName)
		if err != nil {
			klog.ErrorS(err, "skipping device with unsupported product",
				"device", device.Name,
				"product", device.ProductName,
			)
			continue
		}
		group, ok := groups[resourceName]
		if !ok {
			group = DeviceGroup{
				ResourceName: resourceName,
				Devices:      make(map[string]NPUDevice),
			}
		}
		group.Devices[device.Name] = NPUDevice{
			Info:   device,
			Health: healthForDevice(device.Name),
		}
		groups[resourceName] = group
	}

	return groups
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
