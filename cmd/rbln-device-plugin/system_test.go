package main

import (
	"context"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

func testNativeNodes(context.Context, string) ([]system.DeviceNode, error) {
	return []system.DeviceNode{{Path: "/dev/null", HostPath: "/dev/null", Kind: "native"}}, nil
}
