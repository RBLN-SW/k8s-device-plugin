package main

import (
	"context"
	"fmt"
	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
	host "github.com/rbln-sw/rblnlib-go/pkg/host/v1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

var sriovPCISysfsDevicesPath = "/sys/bus/pci/devices"

type sriovClass int

const (
	// sriovClassNone is a device that is not SR-IOV partitioned: a PF with
	// sriov_numvfs == 0, hardware without SR-IOV support, or a device whose
	// sysfs entry is absent.
	sriovClassNone sriovClass = iota
	// sriovClassVF is an SR-IOV virtual function (physfn symlink present).
	sriovClassVF
	// sriovClassHostingPF is a physical function currently hosting VFs
	// (sriov_numvfs > 0).
	sriovClassHostingPF
)

type sriovInfo struct {
	class sriovClass
	// parentPFBusID is the PCI address of the parent PF; set only for
	// sriovClassVF.
	parentPFBusID string
	// numVFs is the hosting PF's sriov_numvfs value; for a VF it is read from
	// the parent PF.
	numVFs int
}

// probeSRIOV projects library facts into the plugin's resource-selection policy.
func probeSRIOV(pciBusID string) (sriovInfo, error) {
	c, err := host.New(host.Options{PCIDevicesRoot: sriovPCISysfsDevicesPath})
	if err != nil {
		return sriovInfo{}, err
	}
	info, err := c.ReadSRIOV(context.Background(), pciBusID)
	if err != nil {
		return sriovInfo{}, err
	}
	if info.Kind == system.VirtualFunction {
		return sriovInfo{class: sriovClassVF, parentPFBusID: info.ParentPCIAddress, numVFs: int(*info.ParentEnabledVFs)}, nil
	}
	if info.EnabledVFs > 0 {
		return sriovInfo{class: sriovClassHostingPF, numVFs: int(info.EnabledVFs)}, nil
	}
	return sriovInfo{class: sriovClassNone}, nil
}

// vfResourceName maps a partition mode (the parent PF's sriov_numvfs) to the
// advertised VF resource. Only the supported partition modes vf-1 and vf-4
// are accepted; any other value is an error so the caller fails closed
// instead of advertising an unsupported configuration.
func vfResourceName(numVFs int) (string, error) {
	switch numVFs {
	case 1, 4:
		return fmt.Sprintf("%s%d", consts.VFResourceNamePrefix, numVFs), nil
	default:
		return "", fmt.Errorf("unsupported SR-IOV partition mode with %d VFs per PF: supported modes are 1 and 4", numVFs)
	}
}
