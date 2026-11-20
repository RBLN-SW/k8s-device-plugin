package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"golang.org/x/sys/unix"
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
	alias := filepath.Join(p.rsdAliasDir, rsdAliasName(buses))
	// Keep alias invalidation, driver regrouping and publication in one
	// transaction across all resource endpoints on the node.
	dir := filepath.Dir(alias)
	staging, err := stagePublication(dir)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "stage RSD device: %v", err)
	}
	defer removeStaging(staging)
	lock, err := lockRSD(ctx, filepath.Join(filepath.Dir(dir), ".rbln-rsd-writer.lock"))
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "lock RSD writer: %v", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			p.log.Warn("Close RSD writer lock", "err", err)
		}
	}()

	legacy, err := p.cachedRSD(req.GetDevicesIds(), devices, alias)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	// For a cached direct path, verify ownership before retiring any aliases.
	target := legacy
	if target != "" {
		if err := verifySystemRSD(ctx, target, buses); err != nil {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
	}
	if err := retireOverlappingAliases(staging, buses); err != nil {
		return nil, status.Errorf(codes.Internal, "retire overlapping RSD aliases: %v", err)
	}
	if target == "" {
		target, err = p.rsdGroupFn(ctx, buses)
		if err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "prepare RSD group for %v: %v", buses, err)
		}
	}
	if err := publishRSDNode(staging, target, alias); err != nil {
		return nil, status.Errorf(codes.Internal, "publish RSD device %s: %v", alias, err)
	}
	p.log.Info("Prepared RSD device before container start", "busIDs", buses, "deviceIDs", req.GetDevicesIds(), "hostRsdPath", target, "alias", alias)
	return &pluginapi.PreStartContainerResponse{}, nil
}

// The private directory is on the host devtmpfs. A hard link presents the
// driver's actual character node to either CRI without relying on symlink
// handling. Atomic replacement also keeps a failed publication from replacing
// a previously valid alias for the same set.
func stagePublication(dir string) (string, error) {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !ok || int(st.Uid) != os.Geteuid() {
		return "", fmt.Errorf("invalid private RSD directory %s", dir)
	}
	return os.MkdirTemp(dir, ".publish-")
}

func removeStaging(staging string) {
	if err := os.RemoveAll(staging); err != nil {
		slog.Warn("Failed to remove RSD staging directory", "path", staging, "err", err)
	}
}

func publishRSDNode(staging, target, alias string) error {
	temp := filepath.Join(staging, "rsd")
	if err := os.Link(target, temp); err != nil {
		return err
	}
	return os.Rename(temp, alias)
}

func lockRSD(ctx context.Context, path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			break
		}
		select {
		case <-time.After(25 * time.Millisecond):
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), f.Close())
		}
	}
	return nil, errors.Join(err, f.Close())
}
