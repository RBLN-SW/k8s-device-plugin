package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	topologyPCISysfsDevicesPath = "/sys/bus/pci/devices"
	topologyPCIAddressPattern   = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-7]$`)
	topologyPCIRootPattern      = regexp.MustCompile(`^pci[0-9a-fA-F]{4}:[0-9a-fA-F]{2}$`)
)

// pciTree is a forest: separate PCI host bridges do not have an invented
// common parent. Shared ancestors are interned by their canonical sysfs path.
// It describes the running OS's PCI hierarchy, including in a VM.
type pciTree struct {
	devices map[string]*pciTreeNode
	nodes   []*pciTreeNode
	roots   []*pciTreeNode
}

type pciTreeNode struct {
	parent   *pciTreeNode
	root     *pciTreeNode
	children []*pciTreeNode
	depth    int
	index    int
}

type pciDistance struct {
	known         bool
	separateRoots bool
	hops          int
}

func newPCITree(devices map[string]NPUDevice) *pciTree {
	tree := &pciTree{devices: make(map[string]*pciTreeNode, len(devices))}
	nodes := make(map[string]*pciTreeNode)
	for _, id := range sortedDeviceIDs(devices) {
		ancestors, err := pciDeviceAncestors(devices[id].Info.PCIBusID)
		if err != nil {
			// Only this device loses PCI ranking; SID, NUMA, and the other
			// devices' paths remain usable. No SMI or RSD topology is read.
			continue
		}
		var parent *pciTreeNode
		for _, path := range ancestors {
			node := nodes[path]
			if node == nil {
				node = &pciTreeNode{parent: parent, index: len(tree.nodes)}
				tree.nodes = append(tree.nodes, node)
				if parent == nil {
					node.root = node
					tree.roots = append(tree.roots, node)
				} else {
					node.root = parent.root
					node.depth = parent.depth + 1
					parent.children = append(parent.children, node)
				}
				nodes[path] = node
			}
			parent = node
		}
		tree.devices[id] = parent
	}
	return tree
}

// pciDeviceAncestors returns the PCI root, all upstream PCI devices, and the
// NPU endpoint. The endpoint is a leaf, never a product-specific "bridge index".
func pciDeviceAncestors(busID string) ([]string, error) {
	if !topologyPCIAddressPattern.MatchString(busID) {
		return nil, fmt.Errorf("invalid PCI address %q", busID)
	}
	path, err := filepath.EvalSymlinks(filepath.Join(topologyPCISysfsDevicesPath, strings.ToLower(busID)))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || !strings.EqualFold(filepath.Base(path), busID) {
		return nil, fmt.Errorf("PCI path does not end at device %s", busID)
	}
	var ancestors []string
	current := string(filepath.Separator)
	for _, segment := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		if topologyPCIRootPattern.MatchString(segment) && len(ancestors) == 0 {
			ancestors = append(ancestors, current)
		} else if len(ancestors) > 0 {
			if !topologyPCIAddressPattern.MatchString(segment) {
				return nil, fmt.Errorf("unexpected component in PCI hierarchy: %s", segment)
			}
			ancestors = append(ancestors, current)
		}
	}
	if len(ancestors) < 2 {
		return nil, fmt.Errorf("PCI root is missing for %s", busID)
	}
	return ancestors, nil
}

func (t *pciTree) distance(source, destination string) pciDistance {
	a, b := t.devices[source], t.devices[destination]
	if a == nil || b == nil {
		return pciDistance{}
	}
	if a.root != b.root {
		return pciDistance{separateRoots: true}
	}
	depthSum := a.depth + b.depth
	for a.depth > b.depth {
		a = a.parent
	}
	for b.depth > a.depth {
		b = b.parent
	}
	for a != b {
		a, b = a.parent, b.parent
	}
	return pciDistance{known: true, hops: depthSum - 2*a.depth}
}
