package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
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

// probeSRIOV classifies a device from sysfs alone, independent of what
// rbln-smi reports: a physfn symlink marks a VF, sriov_numvfs > 0 marks a
// VF-hosting PF. This is the same physfn-based classification rbln-smd uses.
// Indeterminate sysfs states (e.g. physfn present but the parent's
// sriov_numvfs unreadable) are returned as errors so callers exclude the
// device instead of misadvertising it.
func probeSRIOV(pciBusID string) (sriovInfo, error) {
	if pciBusID == "" {
		return sriovInfo{class: sriovClassNone}, nil
	}

	physfnPath := filepath.Join(sriovPCISysfsDevicesPath, pciBusID, "physfn")
	if _, err := os.Lstat(physfnPath); err == nil {
		target, err := os.Readlink(physfnPath)
		if err != nil {
			return sriovInfo{}, fmt.Errorf("read physfn symlink of %s: %w", pciBusID, err)
		}
		parentPFBusID := filepath.Base(target)
		numVFs, err := readSriovNumVFs(parentPFBusID)
		if err != nil {
			return sriovInfo{}, fmt.Errorf("read sriov_numvfs of parent PF %s for VF %s: %w", parentPFBusID, pciBusID, err)
		}
		return sriovInfo{class: sriovClassVF, parentPFBusID: parentPFBusID, numVFs: numVFs}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return sriovInfo{}, fmt.Errorf("stat physfn of %s: %w", pciBusID, err)
	}

	numVFs, err := readSriovNumVFs(pciBusID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sriovInfo{class: sriovClassNone}, nil
		}
		return sriovInfo{}, fmt.Errorf("read sriov_numvfs of %s: %w", pciBusID, err)
	}
	if numVFs > 0 {
		return sriovInfo{class: sriovClassHostingPF, numVFs: numVFs}, nil
	}
	return sriovInfo{class: sriovClassNone}, nil
}

func readSriovNumVFs(pciBusID string) (int, error) {
	numVFsPath := filepath.Join(sriovPCISysfsDevicesPath, pciBusID, "sriov_numvfs")
	raw, err := os.ReadFile(numVFsPath)
	if err != nil {
		// Returned unwrapped: probeSRIOV distinguishes a missing
		// sriov_numvfs attribute via errors.Is(err, os.ErrNotExist).
		return 0, err
	}
	numVFs, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", numVFsPath, err)
	}
	return numVFs, nil
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

func isVFResourceName(resourceName string) bool {
	return strings.HasPrefix(resourceName, consts.VFResourceNamePrefix)
}
