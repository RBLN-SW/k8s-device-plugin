package main

// pciPlacementCost orders forests by cross-root pairs, then within-root hops.
// It deliberately ignores SID, NUMA and diameter constraints: relaxing those
// constraints supplies an admissible bound for the exact combination search.
type pciPlacementCost struct {
	separateRoots int
	totalDistance int
	valid         bool
}

func (a pciPlacementCost) less(b pciPlacementCost) bool {
	return a.valid && (!b.valid || a.separateRoots < b.separateRoots ||
		(a.separateRoots == b.separateRoots && a.totalDistance < b.totalDistance))
}

// pciLowerBound computes the best possible PCI cost of a completion with the
// fewest missing PCI devices. A completion with more missing paths is already
// worse in the preceding score field, irrespective of this bound.
func (s *allocationSearch) pciLowerBound(start, knownCount int) pciPlacementCost {
	tree := s.allocator.tree
	forced, free := make([]int, len(tree.nodes)), make([]int, len(tree.nodes))
	for _, index := range s.chosen {
		if node := tree.devices[s.allocator.devices[index].id]; node != nil {
			forced[node.index]++
		}
	}
	for _, index := range s.candidates[start:] {
		if node := tree.devices[s.allocator.devices[index].id]; node != nil {
			free[node.index]++
		}
	}
	forcedSubtree, capacity := append([]int(nil), forced...), make([]int, len(tree.nodes))
	for i := len(tree.nodes) - 1; i >= 0; i-- {
		node := tree.nodes[i]
		capacity[i] += forced[i] + free[i]
		if node.parent != nil {
			forcedSubtree[node.parent.index] += forcedSubtree[i]
			capacity[node.parent.index] += capacity[i]
		}
	}

	forest := make([]pciPlacementCost, knownCount+1)
	forest[0].valid = true
	totalCapacity, totalForced := 0, 0
	for _, root := range tree.roots {
		totalCapacity += capacity[root.index]
		totalForced += forcedSubtree[root.index]
	}
	for _, root := range tree.roots {
		if capacity[root.index] == 0 {
			continue
		}
		rootCosts := make([]int, min(capacity[root.index], knownCount)+1)
		minimum := max(forcedSubtree[root.index], knownCount-(totalCapacity-capacity[root.index]))
		maximum := min(capacity[root.index], knownCount-(totalForced-forcedSubtree[root.index]))
		for count := range rootCosts {
			rootCosts[count] = impossiblePCICost
			if count < minimum || count > maximum {
				continue
			}
			costs := pciSubtreeCosts(root, count, forced, free, forcedSubtree, capacity)
			rootCosts[count] = costs[count]
		}
		next := make([]pciPlacementCost, knownCount+1)
		for before, previous := range forest {
			if !previous.valid {
				continue
			}
			for count, cost := range rootCosts {
				if before+count > knownCount || cost == impossiblePCICost {
					continue
				}
				candidate := pciPlacementCost{
					separateRoots: previous.separateRoots + before*count,
					totalDistance: previous.totalDistance + cost,
					valid:         true,
				}
				if candidate.less(next[before+count]) {
					next[before+count] = candidate
				}
			}
		}
		forest = next
	}
	return forest[knownCount]
}

const impossiblePCICost = int(^uint(0)>>1) / 4

// For a root containing K selected endpoints, an edge whose subtree contains c
// selected endpoints contributes c*(K-c) to the sum of all pairwise distances.
// Knapsack over the children minimizes that sum without enumerating subsets.
// "forced" are mandatory or already chosen endpoints; "free" can still be used.
func pciSubtreeCosts(node *pciTreeNode, rootCount int, forced, free, forcedSubtree, capacity []int) []int {
	limit := min(capacity[node.index], rootCount)
	costs := make([]int, limit+1)
	for count := range costs {
		costs[count] = impossiblePCICost
		if count >= forced[node.index] && count <= forced[node.index]+free[node.index] {
			costs[count] = 0
		}
	}
	usedCapacity := min(forced[node.index]+free[node.index], rootCount)
	for _, child := range node.children {
		if capacity[child.index] == 0 {
			continue
		}
		childCosts := pciSubtreeCosts(child, rootCount, forced, free, forcedSubtree, capacity)
		next := make([]int, limit+1)
		for count := range next {
			next[count] = impossiblePCICost
		}
		for before := 0; before <= usedCapacity; before++ {
			if costs[before] == impossiblePCICost {
				continue
			}
			for count := forcedSubtree[child.index]; count < len(childCosts) && before+count <= limit; count++ {
				if childCosts[count] == impossiblePCICost {
					continue
				}
				cost := costs[before] + childCosts[count] + count*(rootCount-count)
				next[before+count] = min(next[before+count], cost)
			}
		}
		usedCapacity = min(limit, usedCapacity+capacity[child.index])
		costs = next
	}
	return costs
}
