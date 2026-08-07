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
	// ParentPFBusID is the PCI address of the parent physical function when
	// this device is an SR-IOV VF; empty otherwise.
	ParentPFBusID string
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
		sriov, err := probeSRIOV(device.PCIBusID)
		if err != nil {
			klog.ErrorS(err, "skipping device with indeterminate SR-IOV state",
				"device", device.Name,
				"pciBusID", device.PCIBusID,
			)
			continue
		}
		resourceName, advertise, err := resourceForDevice(device, sriov, useGenericResourceName)
		if err != nil {
			klog.ErrorS(err, "skipping device",
				"device", device.Name,
				"product", device.ProductName,
			)
			continue
		}
		if !advertise {
			klog.InfoS("excluding VF-hosting PF from advertisement",
				"device", device.Name,
				"pciBusID", device.PCIBusID,
				"numVFs", sriov.numVFs,
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
			Info:          device,
			Health:        healthForDevice(device.Name),
			ParentPFBusID: sriov.parentPFBusID,
		}
		groups[resourceName] = group
	}

	return groups
}

// resourceForDevice maps a device's SR-IOV classification to the resource it
// is advertised under. Every device falls into exactly one of three classes:
//
//  1. VF (physfn present): always rebellions.ai/npu-vf<N>, where N is the
//     parent PF's sriov_numvfs, regardless of useGenericResourceName.
//  2. Non-partitioned PF (sriov_numvfs == 0 or absent): the pre-existing
//     product-based resource naming, unchanged.
//  3. VF-hosting PF (sriov_numvfs > 0): not advertised. In the current
//     driver generation, enabling SR-IOV removes the PF from the RSD compute
//     topology (its npu_id disappears), so it is not a usable compute
//     endpoint; advertising it alongside its VFs would double-count compute
//     that does not exist.
//
// If a future partial-partitioning mode lets a PF stay usable with its
// remaining chiplets, only branch 3 should change: decide whether the PF is
// usable from a real signal (e.g. presence of npu_id in rbln-smd v1
// DeviceInfo) and let usable hosting PFs fall through to branch 2, at which
// point the PF (existing resource name) and its VFs (npu-vf<N>) are
// advertised at the same time. Do not implement that today — the current
// driver cannot produce that state.
func resourceForDevice(device rblndevice.Device, sriov sriovInfo, useGenericResourceName bool) (string, bool, error) {
	switch sriov.class {
	case sriovClassVF:
		resourceName, err := vfResourceName(sriov.numVFs)
		if err != nil {
			return "", false, fmt.Errorf("VF %s (parent PF %s): %w", device.Name, sriov.parentPFBusID, err)
		}
		return resourceName, true, nil
	case sriovClassHostingPF:
		return "", false, nil
	default:
		resourceName, err := resourceNameForProduct(device.ProductName, useGenericResourceName)
		if err != nil {
			return "", false, err
		}
		return resourceName, true, nil
	}
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
