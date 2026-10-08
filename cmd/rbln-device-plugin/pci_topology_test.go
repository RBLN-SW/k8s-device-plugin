package main

import (
	"context"
	"fmt"
	"math/bits"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

type pciTestFixture struct {
	devices map[string]NPUDevice
	paths   map[string][]string
}

func newPCITestFixture(t testing.TB, paths map[string]string) pciTestFixture {
	t.Helper()
	root := t.TempDir()
	bus := filepath.Join(root, "bus", "pci", "devices")
	if err := os.MkdirAll(bus, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := topologyPCISysfsDevicesPath
	topologyPCISysfsDevicesPath = bus
	t.Cleanup(func() { topologyPCISysfsDevicesPath = previous })
	fixture := pciTestFixture{devices: make(map[string]NPUDevice), paths: make(map[string][]string)}
	for id, relative := range paths {
		bdf := ""
		if relative != "" {
			target := filepath.Join(root, "devices", filepath.FromSlash(relative))
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			bdf = filepath.Base(target)
			if err := os.Symlink(target, filepath.Join(bus, bdf)); err != nil {
				t.Fatal(err)
			}
			fixture.paths[id] = strings.Split(relative, "/")
		}
		device := testDevice(id, "sid-"+id, bdf, "0")
		device.Info.ProductName = "RBLN-CR13"
		device.Info.PCIDeviceID = "2130"
		fixture.devices[id] = device
	}
	return fixture
}

func standardPCITestFixture(t testing.TB) pciTestFixture {
	t.Helper()
	return newPCITestFixture(t, map[string]string{
		"rbln0": "pci0000:00/0000:00:01.0/0000:01:00.0/0000:02:01.0/0000:03:00.0",
		"rbln1": "pci0000:00/0000:00:01.0/0000:01:00.0/0000:02:02.0/0000:04:00.0",
		"rbln2": "pci0000:00/0000:00:01.0/0000:01:00.0/0000:02:01.0/0000:03:00.1",
		"rbln3": "pci0000:00/0000:00:01.0/0000:01:00.0/0000:02:02.0/0000:04:00.1",
		"rbln4": "pci0000:00/0000:00:02.0/0000:05:00.0",
		"rbln5": "pci000a:00/000a:00:01.0/000a:01:00.0",
		"rbln6": "",
	})
}

func TestPCITreeLCADistances(t *testing.T) {
	fixture := standardPCITestFixture(t)
	tree := newPCITree(fixture.devices)
	tests := []struct {
		a, b string
		want pciDistance
	}{
		{"rbln0", "rbln0", pciDistance{known: true}},
		{"rbln0", "rbln2", pciDistance{known: true, hops: 2}},
		{"rbln0", "rbln1", pciDistance{known: true, hops: 4}},
		{"rbln0", "rbln4", pciDistance{known: true, hops: 6}},
		{"rbln0", "rbln5", pciDistance{separateRoots: true}},
		{"rbln0", "rbln6", pciDistance{}},
		{"rbln6", "rbln6", pciDistance{}},
	}
	for _, tc := range tests {
		if got := tree.distance(tc.a, tc.b); got != tc.want {
			t.Errorf("%s -> %s = %+v, want %+v", tc.a, tc.b, got, tc.want)
		}
		if got := tree.distance(tc.b, tc.a); got != tc.want {
			t.Errorf("reverse %s -> %s = %+v, want %+v", tc.b, tc.a, got, tc.want)
		}
	}
	// Endpoint aliases identify the same PCI device; their distance is zero.
	tree.devices["alias"] = tree.devices["rbln0"]
	if got := tree.distance("rbln0", "alias"); got != (pciDistance{known: true}) {
		t.Fatalf("endpoint alias distance = %+v", got)
	}
}

func TestPCIAncestorsRejectMalformedAndUnavailablePaths(t *testing.T) {
	for _, mode := range []string{"bad address", "missing device", "missing root", "non-PCI ancestor", "wrong endpoint", "file endpoint", "symlink loop"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newPCITestFixture(t, map[string]string{
				"rbln0": "pci0000:00/0000:00:01.0/0000:01:00.0",
			})
			bdf := fixture.devices["rbln0"].Info.PCIBusID
			link := filepath.Join(topologyPCISysfsDevicesPath, bdf)
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "pci0000:00", bdf)
			switch mode {
			case "bad address":
				bdf = "../0000:01:00.0"
			case "missing device":
				bdf = "0000:ff:00.0"
			case "missing root":
				target = filepath.Join(t.TempDir(), bdf)
			case "non-PCI ancestor":
				target = filepath.Join(t.TempDir(), "pci0000:00", "not-a-bridge", bdf)
			case "wrong endpoint":
				target = filepath.Join(t.TempDir(), "pci0000:00", "0000:02:00.0")
			case "symlink loop":
				target = link
			}
			if mode == "file endpoint" {
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			} else if mode != "symlink loop" {
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if got, err := pciDeviceAncestors(bdf); err == nil {
				t.Fatalf("accepted invalid hierarchy: %v", got)
			}
		})
	}
}

func TestPCIAllocationExamples(t *testing.T) {
	tests := []struct {
		name      string
		available []string
		must      []string
		size      int
		configure func(map[string]NPUDevice)
		want      []string
	}{
		{name: "closest pair beats ID order", size: 2, want: []string{"rbln0", "rbln2"}},
		{name: "mandatory device and duplicate IDs", must: []string{"rbln0", "rbln0"}, size: 2, want: []string{"rbln0", "rbln2"}},
		{name: "shared card beats a closer different card", size: 2,
			available: []string{"rbln0", "rbln2", "rbln4"},
			configure: func(d map[string]NPUDevice) { setPCITestSID(d, "card", "rbln0", "rbln4") },
			want:      []string{"rbln0", "rbln4"}},
		{name: "same NUMA beats a closer remote device", must: []string{"rbln0"}, size: 2,
			available: []string{"rbln0", "rbln1", "rbln2"},
			configure: func(d map[string]NPUDevice) { setPCITestNUMA(d, "1", "rbln2") },
			want:      []string{"rbln0", "rbln1"}},
		{name: "one NUMA across cards beats a shared card across NUMAs", must: []string{"rbln0"}, size: 2,
			available: []string{"rbln0", "rbln1", "rbln2", "rbln3"},
			configure: func(d map[string]NPUDevice) {
				setPCITestSID(d, "card-a", "rbln0", "rbln1")
				setPCITestSID(d, "card-b", "rbln2", "rbln3")
				setPCITestNUMA(d, "1", "rbln1", "rbln3")
			}, want: []string{"rbln0", "rbln2"}},
		{name: "known NUMA across cards beats a shared card with unknown NUMA", must: []string{"rbln0"}, size: 2,
			available: []string{"rbln0", "rbln1", "rbln2", "rbln3"},
			configure: func(d map[string]NPUDevice) {
				setPCITestSID(d, "card-a", "rbln0", "rbln1")
				setPCITestSID(d, "card-b", "rbln2", "rbln3")
				setPCITestNUMA(d, "-1", "rbln1", "rbln3")
			}, want: []string{"rbln0", "rbln2"}},
		{name: "known NUMA preferred to unknown", must: []string{"rbln0"}, size: 2,
			available: []string{"rbln0", "rbln1", "rbln2"},
			configure: func(d map[string]NPUDevice) { setPCITestNUMA(d, "N/A", "rbln2") },
			want:      []string{"rbln0", "rbln1"}},
		{name: "unknown NUMA still uses PCI distances", size: 2,
			configure: func(d map[string]NPUDevice) { setPCITestNUMA(d, "-1", sortedDeviceIDs(d)...) },
			want:      []string{"rbln0", "rbln2"}},
		{name: "partial SID selects nearest members", size: 2,
			available: []string{"rbln0", "rbln1", "rbln2"},
			configure: func(d map[string]NPUDevice) { setPCITestSID(d, "card", "rbln0", "rbln1", "rbln2") },
			want:      []string{"rbln0", "rbln2"}},
		{name: "NUMA checked per selected device inside a SID", size: 2,
			available: []string{"rbln0", "rbln1", "rbln2"},
			configure: func(d map[string]NPUDevice) {
				setPCITestSID(d, "card", "rbln0", "rbln1", "rbln2")
				setPCITestNUMA(d, "1", "rbln0")
			}, want: []string{"rbln1", "rbln2"}},
		{name: "zero serials do not create a shared card", size: 2,
			available: []string{"rbln0", "rbln1", "rbln2"},
			configure: func(d map[string]NPUDevice) { setPCITestSID(d, "0000000000000000", "rbln0", "rbln1") },
			want:      []string{"rbln0", "rbln2"}},
		{name: "missing mandatory PCI keeps other distances", must: []string{"rbln6"}, size: 3,
			want: []string{"rbln6", "rbln0", "rbln2"}},
		{name: "separate root never becomes a zero distance", must: []string{"rbln0"}, size: 2,
			available: []string{"rbln0", "rbln4", "rbln5"}, want: []string{"rbln0", "rbln4"}},
		{name: "only kubelet candidates participate", must: []string{"rbln0"}, size: 2,
			available: []string{"rbln0", "rbln1", "rbln4"}, want: []string{"rbln0", "rbln1"}},
		{name: "three device combination", size: 3,
			available: []string{"rbln0", "rbln1", "rbln2", "rbln3"},
			want:      []string{"rbln0", "rbln1", "rbln2"}},
		{name: "no metadata gives deterministic IDs", size: 2,
			configure: func(d map[string]NPUDevice) {
				for id, device := range d {
					device.Info.SID, device.Info.PCINumaNode, device.Info.PCIBusID = "", "", ""
					d[id] = device
				}
			}, want: []string{"rbln0", "rbln1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := standardPCITestFixture(t)
			if tc.configure != nil {
				tc.configure(fixture.devices)
			}
			available := slices.Clone(tc.available)
			if available == nil {
				available = sortedDeviceIDs(fixture.devices)
			}
			plugin := testPluginWithDevices(fixture.devices)
			for pass := 0; pass < 2; pass++ {
				response, err := plugin.GetPreferredAllocation(context.Background(), &pluginapi.PreferredAllocationRequest{
					ContainerRequests: []*pluginapi.ContainerPreferredAllocationRequest{{
						AvailableDeviceIDs: available, MustIncludeDeviceIDs: tc.must, AllocationSize: int32(tc.size),
					}},
				})
				if err != nil || len(response.GetContainerResponses()) != 1 || !slices.Equal(response.ContainerResponses[0].DeviceIDs, tc.want) {
					t.Fatalf("response=%v err=%v, want %v", response, err, tc.want)
				}
				slices.Reverse(available)
				available = append(available, available[0])
			}
		})
	}
}

func TestPCIAllocationPrefersOneNUMAAcrossThreeFragmentedCards(t *testing.T) {
	paths := make(map[string]string)
	for i := 0; i < 7; i++ {
		paths[fmt.Sprintf("rbln%d", i)] = fmt.Sprintf("pci0000:00/0000:00:01.0/0000:%02x:00.0", i+16)
	}
	fixture := newPCITestFixture(t, paths)
	setPCITestSID(fixture.devices, "card-b", "rbln1", "rbln2")
	setPCITestSID(fixture.devices, "card-d", "rbln4", "rbln5", "rbln6")
	setPCITestNUMA(fixture.devices, "1", "rbln4", "rbln5", "rbln6")
	plugin := testPluginWithDevices(fixture.devices)
	// NUMA 0 offers 1+2+1 devices across three cards. Two cards could satisfy
	// the request only by crossing NUMA nodes; that must lose despite using fewer cards.
	for _, must := range [][]string{nil, {"rbln0"}} {
		got, err := plugin.selectPreferredDeviceIDs(sortedDeviceIDs(fixture.devices), must, 4)
		if want := []string{"rbln0", "rbln1", "rbln2", "rbln3"}; err != nil || !slices.Equal(got, want) {
			t.Fatalf("must=%v: selected %v err=%v, want %v", must, got, err, want)
		}
	}
}

func TestPCIAllocationMinimizesMaximumBeforeTotal(t *testing.T) {
	fixture := newPCITestFixture(t, map[string]string{
		"rbln0": "pci0000:00/0000:00:01.0/0000:01:01.0/0000:02:01.0/0000:03:00.0",
		"rbln1": "pci0000:00/0000:00:01.0/0000:01:02.0/0000:04:01.0/0000:05:00.0",
		"rbln2": "pci0000:00/0000:00:01.0/0000:01:01.0/0000:02:01.0/0000:03:01.0/0000:06:00.0",
		"rbln3": "pci0000:00/0000:00:01.0/0000:01:03.0/0000:07:01.0/0000:08:00.0",
	})
	// With mandatory 0/1, adding 2 gives (max=7,total=16), while adding
	// 3 gives (max=6,total=18). The smaller diameter wins.
	got := newTopologyAllocator(fixture.devices).SelectDevices([]string{"rbln0", "rbln1"}, 3)
	if want := []string{"rbln0", "rbln1", "rbln3"}; !slices.Equal(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
}

func TestPCIAllocationTotalBreaksMaximumTie(t *testing.T) {
	fixture := newPCITestFixture(t, map[string]string{
		"rbln0": "pci0000:00/0000:00:01.0/0000:01:01.0/0000:02:01.0/0000:03:01.0/0000:04:00.0",
		"rbln1": "pci0000:00/0000:00:01.0/0000:01:02.0/0000:05:01.0/0000:06:01.0/0000:07:00.0",
		"rbln2": "pci0000:00/0000:00:01.0/0000:01:03.0/0000:08:01.0/0000:09:00.0",
		"rbln3": "pci0000:00/0000:00:01.0/0000:01:01.0/0000:02:01.0/0000:03:00.0",
	})
	// The mandatory pair fixes max=8. Adding 2 totals 22; adding 3 totals 18.
	got := newTopologyAllocator(fixture.devices).SelectDevices([]string{"rbln0", "rbln1"}, 3)
	if want := []string{"rbln0", "rbln1", "rbln3"}; !slices.Equal(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
}

func TestPCIAllocationSurvivesOneBrokenSymlink(t *testing.T) {
	fixture := standardPCITestFixture(t)
	if err := os.Remove(filepath.Join(topologyPCISysfsDevicesPath, fixture.devices["rbln1"].Info.PCIBusID)); err != nil {
		t.Fatal(err)
	}
	got := newTopologyAllocator(fixture.devices).SelectDevices([]string{"rbln0"}, 2)
	if want := []string{"rbln0", "rbln2"}; !slices.Equal(got, want) {
		t.Fatalf("selected %v, want remaining topology to choose %v", got, want)
	}
}

func TestPCIAllocationMatchesExhaustiveOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(41))
	for trial := 0; trial < 80; trial++ {
		t.Run(fmt.Sprint(trial), func(t *testing.T) {
			paths := make(map[string]string)
			for i := 0; i < 7; i++ {
				domain, bridge := rng.Intn(3), rng.Intn(3)+1
				path := fmt.Sprintf("pci%04x:00/%04x:00:%02x.0", domain, domain, bridge)
				if rng.Intn(2) == 0 {
					path += fmt.Sprintf("/%04x:%02x:00.0", domain, bridge)
				}
				path += fmt.Sprintf("/%04x:%02x:00.0", domain, i+16)
				if rng.Intn(5) == 0 {
					path = ""
				}
				paths[fmt.Sprintf("rbln%d", i)] = path
			}
			fixture := newPCITestFixture(t, paths)
			ids := sortedDeviceIDs(fixture.devices)
			for _, id := range ids {
				device := fixture.devices[id]
				device.Info.SID = []string{"a", "b", "c", "", "0000000000000000"}[rng.Intn(5)]
				device.Info.PCINumaNode = []string{"0", "1", "-1"}[rng.Intn(3)]
				fixture.devices[id] = device
			}
			if trial%6 == 0 {
				// Multiple logical devices may name the same PCI endpoint.
				device := fixture.devices["rbln6"]
				device.Info.PCIBusID = fixture.devices["rbln0"].Info.PCIBusID
				fixture.devices["rbln6"] = device
				fixture.paths["rbln6"] = fixture.paths["rbln0"]
			}
			allocator := newTopologyAllocator(fixture.devices)
			for size := 1; size <= len(ids); size++ {
				var must []string
				order := rng.Perm(len(ids))
				for _, index := range order[:rng.Intn(min(size, 3)+1)] {
					must = append(must, ids[index])
				}
				got := allocator.SelectDevices(must, size)
				want := exhaustivePCISelection(fixture, must, size)
				if !slices.Equal(got, want) {
					t.Fatalf("size=%d must=%v: selected %v, exhaustive optimum %v", size, must, got, want)
				}
			}
		})
	}
}

// The oracle enumerates bitsets without pruning and computes distances from
// path prefixes, independently of the production tree and incremental scorer.
func exhaustivePCISelection(fixture pciTestFixture, must []string, size int) []string {
	ids := sortedDeviceIDs(fixture.devices)
	var best []string
	var bestScore []int
	for mask := 0; mask < 1<<len(ids); mask++ {
		if bits.OnesCount(uint(mask)) != size {
			continue
		}
		var chosen []string
		for i, id := range ids {
			if mask&(1<<i) != 0 {
				chosen = append(chosen, id)
			}
		}
		matches := true
		for _, id := range must {
			matches = matches && slices.Contains(chosen, id)
		}
		if !matches {
			continue
		}
		sids, numas := map[string]bool{}, map[string]bool{}
		score := make([]int, 7)
		for i, id := range chosen {
			info := fixture.devices[id].Info
			sid := info.SID
			if sid == "" || sid == "0000000000000000" {
				sid = "missing:" + id
			}
			sids[sid] = true
			if info.PCINumaNode == "-1" {
				score[0]++
			} else {
				numas[info.PCINumaNode] = true
			}
			a := fixture.paths[id]
			if len(a) == 0 {
				score[3]++
				continue
			}
			for _, other := range chosen[:i] {
				b := fixture.paths[other]
				if len(b) == 0 {
					continue
				}
				if a[0] != b[0] {
					score[4]++
					continue
				}
				common := 0
				for common < min(len(a), len(b)) && a[common] == b[common] {
					common++
				}
				distance := len(a) + len(b) - 2*common
				score[5] = max(score[5], distance)
				score[6] += distance
			}
		}
		score[1], score[2] = len(numas), len(sids)
		if best == nil || slices.Compare(score, bestScore) < 0 ||
			(slices.Equal(score, bestScore) && slices.Compare(chosen, best) < 0) {
			best, bestScore = chosen, score
		}
	}
	result := slices.Clone(must)
	for _, id := range best {
		if !slices.Contains(must, id) {
			result = append(result, id)
		}
	}
	return result
}

func TestPCIAllocationRequestConstraints(t *testing.T) {
	withoutPCITopology(t)
	plugin := testPluginWithDevices(map[string]NPUDevice{
		"rbln0": testDevice("rbln0", "", "", ""),
		"rbln1": testDevice("rbln1", "", "", ""),
		"rbln2": testDevice("rbln2", "", "", ""),
	})
	tests := []struct {
		name      string
		available []string
		must      []string
		size      int
		want      []string
		wantErr   bool
	}{
		{name: "negative size", size: -1, wantErr: true},
		{name: "empty allocation"},
		{name: "zero size cannot omit mandatory device", available: []string{"rbln0"}, must: []string{"rbln0"}, wantErr: true},
		{name: "no candidates", size: 1, wantErr: true},
		{name: "unknown available device", available: []string{"missing"}, size: 1, wantErr: true},
		{name: "mandatory device not available", available: []string{"rbln0"}, must: []string{"rbln1"}, size: 1, wantErr: true},
		{name: "duplicate availability cannot satisfy size", available: []string{"rbln0", "rbln0"}, size: 2, wantErr: true},
		{name: "duplicate mandatory device", available: []string{"rbln1", "rbln0"}, must: []string{"rbln0", "rbln0"}, size: 1, want: []string{"rbln0"}},
		{name: "all devices", available: []string{"rbln2", "rbln0", "rbln1"}, must: []string{"rbln2"}, size: 3, want: []string{"rbln2", "rbln0", "rbln1"}},
		{name: "all mandatory", available: []string{"rbln0", "rbln1", "rbln2"}, must: []string{"rbln2", "rbln1"}, size: 2, want: []string{"rbln2", "rbln1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := plugin.selectPreferredDeviceIDs(tc.available, tc.must, tc.size)
			if (err != nil) != tc.wantErr || (!tc.wantErr && !slices.Equal(got, tc.want)) {
				t.Fatalf("selected=%v err=%v, want=%v error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func setPCITestSID(devices map[string]NPUDevice, sid string, ids ...string) {
	for _, id := range ids {
		device := devices[id]
		device.Info.SID = sid
		devices[id] = device
	}
}

func setPCITestNUMA(devices map[string]NPUDevice, numa string, ids ...string) {
	for _, id := range ids {
		device := devices[id]
		device.Info.PCINumaNode = numa
		devices[id] = device
	}
}

func BenchmarkPCIAllocation(b *testing.B) {
	for _, layout := range []string{"flat", "missing", "clusters"} {
		b.Run("32_devices_select_16_"+layout, func(b *testing.B) {
			paths := make(map[string]string)
			for i := 0; i < 32; i++ {
				path := fmt.Sprintf("pci0000:00/0000:00:01.0/0000:01:%02x.0", i)
				if layout == "clusters" {
					path = fmt.Sprintf("pci0000:00/0000:00:01.0/0000:01:%02x.0/0000:%02x:%02x.0", i/12, i/12+2, i%12)
				}
				paths[fmt.Sprintf("rbln%02d", i)] = path
			}
			if layout == "missing" {
				paths["rbln00"] = ""
			}
			fixture := newPCITestFixture(b, paths)
			allocator := newTopologyAllocator(fixture.devices)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := allocator.SelectDevices(nil, 16); len(got) != 16 {
					b.Fatal(got)
				}
			}
		})
	}
}
