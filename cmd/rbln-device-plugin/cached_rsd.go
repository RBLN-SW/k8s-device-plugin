package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	smi "github.com/rbln-sw/rblnlib-go/pkg/smi/v1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"google.golang.org/protobuf/proto"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func (p *ResourcePlugin) identityKey() string {
	return "rebellions.ai/pci-" + filepath.Base(p.resourceName)
}

func pciIdentity(ids []string, devices []NPUDevice) string {
	pairs := make([]string, len(ids))
	for i, id := range ids {
		bus, _ := system.NormalizePCIAddress(devices[i].Info.PCIBusID) // validated by allocationBusIDs
		pairs[i] = id + "=" + bus
	}
	slices.Sort(pairs)
	return strings.Join(pairs, ",")
}

// PreStart carries IDs only. At upgrade the allocation response can still
// reference a direct RSD device. Read kubelet's saved response without ever
// rewriting its checkpoint. Conflicting entries cannot be disambiguated by
// Pod UID at this API, so require all matching entries to be compatible.
func (p *ResourcePlugin) cachedRSD(ids []string, devices []NPUDevice, alias string) (string, error) {
	if p.kubeletSocket == "" {
		return "", nil
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(p.kubeletSocket), "kubelet_internal_checkpoint"))
	if err != nil {
		return "", fmt.Errorf("read kubelet allocation: %w", err)
	}
	var cp struct {
		Data *struct {
			PodDeviceEntries []struct {
				ResourceName string
				DeviceIDs    map[string][]string
				AllocResp    []byte
			}
		}
	}
	if err := json.Unmarshal(raw, &cp); err != nil {
		return "", err
	}
	if cp.Data == nil {
		return "", errors.New("unknown kubelet checkpoint format")
	}
	want := slices.Clone(ids)
	slices.Sort(want)
	var legacy string
	matched := false
	for _, entry := range cp.Data.PodDeviceEntries {
		if entry.ResourceName != p.resourceName {
			continue
		}
		var got []string
		for _, v := range entry.DeviceIDs {
			got = append(got, v...)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			continue
		}
		var response pluginapi.ContainerAllocateResponse
		if err := proto.Unmarshal(entry.AllocResp, &response); err != nil {
			return "", err
		}
		if identity := response.Annotations[p.identityKey()]; identity != "" && identity != pciIdentity(ids, devices) {
			return "", errors.New("PCI identity changed since Allocate; recreate the Pod")
		}
		found := false
		for _, dev := range response.Devices {
			if dev.ContainerPath != "/dev/rsd0" {
				continue
			}
			found = true
			if dev.HostPath == alias {
				continue
			}
			if _, err := legacyGroupID(dev.HostPath); err != nil {
				return "", fmt.Errorf("cached RSD path %q differs from current PCI alias; recreate the Pod", dev.HostPath)
			}
			if legacy != "" && legacy != dev.HostPath {
				return "", errors.New("conflicting cached RSD paths; recreate the Pod")
			}
			legacy = dev.HostPath
		}
		if !found {
			return "", errors.New("cached allocation lacks an RSD device; recreate the Pod")
		}
		matched = true
	}
	if !matched {
		return "", errors.New("allocation is not present in kubelet checkpoint")
	}
	return legacy, nil
}

func legacyGroupID(path string) (uint32, error) {
	n, err := strconv.ParseUint(strings.TrimPrefix(path, "/dev/rsd"), 10, 32)
	if err != nil || n == 0 || fmt.Sprintf("/dev/rsd%d", n) != path {
		return 0, fmt.Errorf("invalid private RSD path %q", path)
	}
	return uint32(n), nil
}

func verifySystemRSD(ctx context.Context, path string, buses []string) error {
	id, err := legacyGroupID(path)
	if err != nil {
		return err
	}
	client, err := smi.New(smi.Options{})
	if err != nil {
		return err
	}
	groups, err := client.ListGroups(ctx)
	if err != nil {
		return err
	}
	if err = smi.ValidateGroupMembers(id, buses, groups); err != nil {
		return fmt.Errorf("cached RSD membership changed; recreate the Pod: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("%s is not a character device", path)
	}
	return nil
}
