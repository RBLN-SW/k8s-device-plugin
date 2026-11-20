package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func allocationBusIDs(devices []NPUDevice) ([]string, error) {
	if len(devices) == 0 {
		return nil, errors.New("empty device request")
	}
	buses := make([]string, 0, len(devices))
	for _, dev := range devices {
		bus, err := system.NormalizePCIAddress(dev.Info.PCIBusID)
		if err != nil {
			return nil, fmt.Errorf("PCI address for %s: %w", dev.Info.Name, err)
		}
		buses = append(buses, bus)
	}
	slices.Sort(buses)
	for i := 1; i < len(buses); i++ {
		if buses[i] == buses[i-1] {
			return nil, fmt.Errorf("duplicate PCI address %s", buses[i])
		}
	}
	return buses, nil
}

func (p *ResourcePlugin) PreStartContainer(ctx context.Context, req *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	devices, err := p.selectedDevices(req.GetDevicesIds())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	buses, err := allocationBusIDs(devices)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Concurrent containers receive disjoint exclusive devices, so their
	// lowest PCI addresses differ. Sequential phases can reuse a path. Both
	// callbacks derive it from inventory, including after plugin restart.
	alias := filepath.Join(p.rsdAliasDir, buses[0])
	target, err := p.rsdGroupFn(ctx, buses)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "prepare RSD group for %v: %v", buses, err)
	}
	if err := publishRSDNode(target, alias); err != nil {
		return nil, status.Errorf(codes.Internal, "publish RSD device %s: %v", alias, err)
	}
	p.log.Info("Prepared RSD device before container start", "busIDs", buses, "deviceIDs", req.GetDevicesIds(), "hostRsdPath", target, "alias", alias)
	return &pluginapi.PreStartContainerResponse{}, nil
}

// The private directory is on the host devtmpfs. A hard link presents the
// driver's actual character node to either CRI without relying on symlink
// handling. Atomic replacement also keeps a failed publication from replacing
// a previously valid alias. Only one name per PCI function is needed.
func publishRSDNode(target, alias string) error {
	dir := filepath.Dir(alias)
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !ok || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("invalid private RSD directory %s", dir)
	}
	staging, err := os.MkdirTemp(dir, ".publish-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	temp := filepath.Join(staging, "rsd")
	if err := os.Link(target, temp); err != nil {
		return err
	}
	return os.Rename(temp, alias)
}
