package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type allocationDevice struct {
	id     string
	sid    int
	numa   int // -1 means unknown; otherwise an index into numaCounts.
	hasPCI bool
}

type topologyAllocator struct {
	devices   []allocationDevice
	byID      map[string]int
	pairs     [][]pciDistance
	tree      *pciTree
	sidCount  int
	numaCount int
}

// NUMA locality precedes shared-card packing; PCI paths refine that placement.
// Unknown metadata is never evidence of a local or zero-hop link.
type allocationScore struct {
	unknownNUMA, numaNodes, sidGroups int
	unknownPCI, separateRoots         int
	maxDistance, totalDistance        int
}

func (s allocationScore) less(other allocationScore) bool {
	return slices.Compare(s.values(), other.values()) < 0
}

func (s allocationScore) values() []int {
	return []int{s.unknownNUMA, s.numaNodes, s.sidGroups, s.unknownPCI,
		s.separateRoots, s.maxDistance, s.totalDistance}
}

func (p *ResourcePlugin) selectPreferredDeviceIDs(availableDeviceIDs, mustIncludeDeviceIDs []string, allocationSize int) ([]string, error) {
	if allocationSize < 0 {
		return nil, fmt.Errorf("allocation size must be non-negative")
	}
	availableDevices, err := p.availableDevices(availableDeviceIDs)
	if err != nil {
		return nil, err
	}
	if allocationSize > len(availableDevices) {
		return nil, fmt.Errorf("requested %d devices, but only %d are available", allocationSize, len(availableDevices))
	}
	if err := validateMustInclude(availableDevices, mustIncludeDeviceIDs, allocationSize); err != nil {
		return nil, err
	}
	selected, excluded := normalizeMustInclude(mustIncludeDeviceIDs)
	if len(selected) == allocationSize {
		return selected, nil
	}
	// No placement decision, and therefore no sysfs reads, when all are needed.
	if allocationSize == len(availableDevices) {
		for _, id := range sortedDeviceIDs(availableDevices) {
			if _, used := excluded[id]; !used {
				selected = append(selected, id)
			}
		}
		return selected, nil
	}
	selected = newTopologyAllocator(availableDevices).SelectDevices(mustIncludeDeviceIDs, allocationSize)
	if len(selected) != allocationSize {
		return nil, fmt.Errorf("selected %d devices for a request of %d", len(selected), allocationSize)
	}
	return selected, nil
}

func newTopologyAllocator(devices map[string]NPUDevice) *topologyAllocator {
	tree := newPCITree(devices)
	allocator := &topologyAllocator{byID: make(map[string]int, len(devices)), tree: tree}
	sids := make(map[string]int)
	numas := make(map[int]int)
	for _, id := range sortedDeviceIDs(devices) {
		info := devices[id].Info
		sidKey := "sid:" + strings.TrimSpace(info.SID)
		if !knownSID(info.SID) {
			sidKey = "device:" + id
		}
		if _, exists := sids[sidKey]; !exists {
			sids[sidKey] = len(sids)
		}
		numaIndex := -1
		if numa, err := strconv.Atoi(strings.TrimSpace(info.PCINumaNode)); err == nil && numa >= 0 {
			if _, exists := numas[numa]; !exists {
				numas[numa] = len(numas)
			}
			numaIndex = numas[numa]
		}
		allocator.byID[id] = len(allocator.devices)
		allocator.devices = append(allocator.devices, allocationDevice{
			id: id, sid: sids[sidKey], numa: numaIndex, hasPCI: tree.devices[id] != nil,
		})
	}
	allocator.sidCount, allocator.numaCount = len(sids), len(numas)
	allocator.pairs = make([][]pciDistance, len(allocator.devices))
	for i, src := range allocator.devices {
		allocator.pairs[i] = make([]pciDistance, len(allocator.devices))
		for j, dst := range allocator.devices {
			allocator.pairs[i][j] = tree.distance(src.id, dst.id)
		}
	}
	return allocator
}

func knownSID(sid string) bool {
	sid = strings.ToLower(strings.TrimSpace(sid))
	if sid == "" || sid == "n/a" || sid == "unknown" {
		return false
	}
	// SMI can supply a zero-filled serial when the identity is unavailable.
	return strings.Trim(strings.TrimPrefix(sid, "0x"), "0") != ""
}

// SelectDevices searches combinations, including subsets within one SID.
// Bounds only prune choices that cannot improve the best score: this is exact
// for the documented score rather than greedy nearest-neighbor selection.
func (a *topologyAllocator) SelectDevices(mustIncludeDeviceIDs []string, allocationSize int) []string {
	selected, excluded := normalizeMustInclude(mustIncludeDeviceIDs)
	remaining := allocationSize - len(selected)
	if remaining <= 0 {
		return selected
	}
	s := allocationSearch{allocator: a, sidCounts: make([]int, a.sidCount), numaCounts: make([]int, a.numaCount)}
	for _, id := range selected {
		s.add(a.byID[id])
	}
	for i, device := range a.devices {
		if _, used := excluded[device.id]; !used {
			s.candidates = append(s.candidates, i)
		}
	}
	s.prepareBounds()
	s.search(0, remaining)
	result := make([]string, 0, len(s.best))
	for _, index := range s.best {
		result = append(result, a.devices[index].id)
	}
	return result
}

type allocationSearch struct {
	allocator                       *topologyAllocator
	candidates, chosen, best        []int
	sidCounts, numaCounts           []int
	score, bestScore                allocationScore
	suffixSID                       [][]int
	suffixKnownNUMA, suffixKnownPCI []int
	minDistance, minSeparateRoots   int
}

func (s *allocationSearch) add(index int) {
	device := s.allocator.devices[index]
	if s.sidCounts[device.sid] == 0 {
		s.score.sidGroups++
	}
	s.sidCounts[device.sid]++
	if device.numa < 0 {
		s.score.unknownNUMA++
	} else {
		if s.numaCounts[device.numa] == 0 {
			s.score.numaNodes++
		}
		s.numaCounts[device.numa]++
	}
	if !device.hasPCI {
		s.score.unknownPCI++
	}
	for _, other := range s.chosen {
		pair := s.allocator.pairs[index][other]
		if pair.separateRoots {
			s.score.separateRoots++
		}
		if pair.known {
			s.score.maxDistance = max(s.score.maxDistance, pair.hops)
			s.score.totalDistance += pair.hops
		}
	}
	s.chosen = append(s.chosen, index)
}

func (s *allocationSearch) search(start, remaining int) {
	if remaining == 0 {
		if s.best == nil || s.score.less(s.bestScore) {
			s.best = append(s.best[:0], s.chosen...)
			s.bestScore = s.score
		}
		return
	}
	if len(s.candidates)-start < remaining {
		return
	}
	if s.best != nil && !s.lowerBound(start, remaining).less(s.bestScore) {
		return
	}
	// Sorted IDs make the first equal-score result the lexicographic winner,
	// independent of map or kubelet candidate order.
	for i := start; i <= len(s.candidates)-remaining; i++ {
		index := s.candidates[i]
		previous := s.score
		s.add(index)
		s.search(i+1, remaining-1)
		s.chosen = s.chosen[:len(s.chosen)-1]
		device := s.allocator.devices[index]
		s.sidCounts[device.sid]--
		if device.numa >= 0 {
			s.numaCounts[device.numa]--
		}
		s.score = previous
	}
}

func (s *allocationSearch) prepareBounds() {
	n := len(s.candidates)
	s.suffixSID = make([][]int, n+1)
	s.suffixSID[n] = make([]int, len(s.sidCounts))
	s.suffixKnownNUMA = make([]int, n+1)
	s.suffixKnownPCI = make([]int, n+1)
	for i := n - 1; i >= 0; i-- {
		device := s.allocator.devices[s.candidates[i]]
		s.suffixSID[i] = slices.Clone(s.suffixSID[i+1])
		s.suffixSID[i][device.sid]++
		s.suffixKnownNUMA[i] = s.suffixKnownNUMA[i+1]
		if device.numa >= 0 {
			s.suffixKnownNUMA[i]++
		}
		s.suffixKnownPCI[i] = s.suffixKnownPCI[i+1]
		if device.hasPCI {
			s.suffixKnownPCI[i]++
		}
	}
	s.minDistance = int(^uint(0) >> 1)
	s.minSeparateRoots = 1
	for i := range s.allocator.devices {
		for j := 0; j < i; j++ {
			pair := s.allocator.pairs[i][j]
			if pair.known {
				s.minDistance = min(s.minDistance, pair.hops)
			}
			if !pair.separateRoots {
				s.minSeparateRoots = 0
			}
		}
	}
	if s.minDistance == int(^uint(0)>>1) {
		s.minDistance = 0
	}
}

func (s *allocationSearch) lowerBound(start, remaining int) allocationScore {
	bound := s.score
	needNew := remaining
	var capacities []int
	for sid, capacity := range s.suffixSID[start] {
		if s.sidCounts[sid] > 0 {
			needNew -= capacity
		} else if capacity > 0 {
			capacities = append(capacities, capacity)
		}
	}
	slices.Sort(capacities)
	for i := len(capacities) - 1; i >= 0 && needNew > 0; i-- {
		bound.sidGroups++
		needNew -= capacities[i]
	}
	bound.unknownNUMA += max(0, remaining-s.suffixKnownNUMA[start])
	bound.unknownPCI += max(0, remaining-s.suffixKnownPCI[start])
	newPairs := remaining*len(s.chosen) + remaining*(remaining-1)/2
	bound.separateRoots += newPairs * s.minSeparateRoots
	knownFinally := len(s.chosen) + remaining - bound.unknownPCI
	knownNow := len(s.chosen) - s.score.unknownPCI
	connectedNow := knownNow*(knownNow-1)/2 - s.score.separateRoots
	connectedFinally := knownFinally*(knownFinally-1)/2 - bound.separateRoots
	cheap := bound
	cheap.totalDistance += max(0, connectedFinally-connectedNow) * s.minDistance
	if connectedFinally > 0 {
		cheap.maxDistance = max(cheap.maxDistance, s.minDistance)
	}
	if !cheap.less(s.bestScore) {
		return cheap
	}
	// A still-better NUMA/SID/availability prefix cannot be pruned by PCI
	// metrics, so avoid computing the more expensive tree bound in that case.
	if slices.Compare(bound.values()[:4], s.bestScore.values()[:4]) < 0 {
		return cheap
	}
	// Any completion needs 'remaining' candidates. Their existing connections
	// to the mandatory/chosen devices bound its diameter from below.
	worst := make([]int, 0, len(s.candidates)-start)
	for _, candidate := range s.candidates[start:] {
		distance := 0
		for _, chosen := range s.chosen {
			distance = max(distance, s.allocator.pairs[candidate][chosen].hops)
		}
		worst = append(worst, distance)
	}
	slices.Sort(worst)
	bound.maxDistance = max(bound.maxDistance, worst[remaining-1])
	if !bound.less(s.bestScore) {
		return bound
	}
	pci := s.pciLowerBound(start, knownFinally)
	bound.separateRoots, bound.totalDistance = pci.separateRoots, pci.totalDistance
	if knownFinally*(knownFinally-1)/2 > bound.separateRoots {
		bound.maxDistance = max(bound.maxDistance, s.minDistance)
	}
	return bound
}

func validateMustInclude(availableDevices map[string]NPUDevice, mustIncludeDeviceIDs []string, allocationSize int) error {
	seen := make(map[string]struct{}, len(mustIncludeDeviceIDs))
	for _, deviceID := range mustIncludeDeviceIDs {
		if _, exists := seen[deviceID]; exists {
			continue
		}
		if _, ok := availableDevices[deviceID]; !ok {
			return fmt.Errorf("preferred allocation must include unknown device %q", deviceID)
		}
		seen[deviceID] = struct{}{}
	}
	if len(seen) > allocationSize {
		return fmt.Errorf("preferred allocation must include %d devices, which exceeds allocation size %d", len(seen), allocationSize)
	}
	return nil
}

func normalizeMustInclude(mustIncludeDeviceIDs []string) ([]string, map[string]struct{}) {
	selected := make([]string, 0, len(mustIncludeDeviceIDs))
	excluded := make(map[string]struct{}, len(mustIncludeDeviceIDs))
	for _, deviceID := range mustIncludeDeviceIDs {
		if _, exists := excluded[deviceID]; exists {
			continue
		}
		excluded[deviceID] = struct{}{}
		selected = append(selected, deviceID)
	}
	return selected, excluded
}
