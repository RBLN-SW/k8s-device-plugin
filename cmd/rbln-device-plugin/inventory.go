/*
Copyright 2026 Rebellions Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
		slog.Error("Device discovery failed; reporting zero devices until it recovers", "err", err)
		return groups
	}

	for _, device := range devices {
		sriov, err := probeSRIOV(device.PCIBusID)
		if err != nil {
			slog.Error("Skipping device with indeterminate SR-IOV state",
				"err", err,
				"device", device.Name,
				"pciBusID", device.PCIBusID,
			)
			continue
		}
		resourceName, advertise, err := resourceForDevice(device, sriov, useGenericResourceName)
		if err != nil {
			if sriov.class == sriovClassVF {
				slog.Error("Skipping VF with unsupported SR-IOV partition mode",
					"err", err,
					"device", device.Name,
					"pciBusID", device.PCIBusID,
					"parentPFBusID", sriov.parentPFBusID,
					"numVFs", sriov.numVFs,
				)
				continue
			}
			if _, seen := unsupportedProductsLogged.LoadOrStore(device.Name+"/"+device.ProductName, struct{}{}); !seen {
				slog.Error("Skipping device with unsupported product; it stays invisible to Kubernetes",
					"err", err,
					"device", device.Name,
					"product", device.ProductName,
				)
			}
			continue
		}
		if !advertise {
			slog.Debug("Excluding VF-hosting PF from advertisement",
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
		health, statusName := healthForDevice(device.Name)
		group.Devices[device.Name] = NPUDevice{
			Info:          device,
			Health:        health,
			StatusName:    statusName,
			ParentPFBusID: sriov.parentPFBusID,
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
