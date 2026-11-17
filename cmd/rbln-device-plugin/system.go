package main

import (
	"context"
	rblndevice "github.com/rbln-sw/rblnlib-go/pkg/device"
	host "github.com/rbln-sw/rblnlib-go/pkg/host/v1"
	smi "github.com/rbln-sw/rblnlib-go/pkg/smi/v1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

func getSystemDevices(ctx context.Context) ([]rblndevice.Device, error) {
	c, err := smi.New(smi.Options{})
	if err != nil {
		return nil, err
	}
	snapshot, err := c.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	if err := system.IssuesError(snapshot.Issues); err != nil {
		return nil, err
	}
	out := make([]rblndevice.Device, 0, len(snapshot.Devices))
	for _, d := range snapshot.Devices {
		out = append(out, rblndevice.FromSystem(d))
	}
	return out, nil
}

func recreateSystemRSDGroup(ctx context.Context, busIDs []string) (string, error) {
	c, err := smi.New(smi.Options{ReplaceIntersectingGroups: true})
	if err != nil {
		return "", err
	}
	group, err := c.RecreateGroup(ctx, busIDs)
	if err != nil {
		return "", err
	}
	machine, err := host.New(host.Options{})
	if err != nil {
		return "", err
	}
	node, err := machine.WaitForNode(ctx, group.Node)
	return node.HostPath, err
}

func nativeSystemNodes(ctx context.Context, address string) ([]system.DeviceNode, error) {
	c, err := host.New(host.Options{})
	if err != nil {
		return nil, err
	}
	return c.WaitForDeviceNodes(ctx, address)
}
