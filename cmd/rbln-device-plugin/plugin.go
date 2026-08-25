package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/rbln-sw/rblnlib-go/pkg/rsdgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	deviceNodePollTimeout  = 5 * time.Second
	deviceNodePollInterval = 100 * time.Millisecond
	registerTimeout        = 10 * time.Second
)

type ResourcePlugin struct {
	pluginapi.UnimplementedDevicePluginServer

	mu            sync.RWMutex
	resourceName  string
	socketPath    string
	kubeletSocket string
	log           *slog.Logger
	cdi           *CDIHandler
	rsdGroupFn    func([]string) (string, error)
	devices       map[string]NPUDevice
	updateCh      chan []*pluginapi.Device
	stopCh        chan struct{}
	server        *grpc.Server
	listener      net.Listener
	wg            sync.WaitGroup
}

func NewResourcePlugin(resourceName, socketPath, kubeletSocket string, cdi *CDIHandler, devices map[string]NPUDevice) *ResourcePlugin {
	return &ResourcePlugin{
		resourceName:  resourceName,
		socketPath:    socketPath,
		kubeletSocket: kubeletSocket,
		// Bound once so every record of this plugin is attributable to its
		// resource. Safe here because plugins are built after the process
		// logger is installed, so this captures the configured handler.
		log:        slog.With("resourceName", resourceName),
		cdi:        cdi,
		rsdGroupFn: rsdgroup.RecreateRsdGroup,
		devices:    cloneDeviceMap(devices),
		updateCh:   make(chan []*pluginapi.Device, 1),
		stopCh:     make(chan struct{}),
	}
}

func (p *ResourcePlugin) Start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(p.socketPath), 0o755); err != nil {
		return err
	}
	if err := os.Remove(p.socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	lis, err := net.Listen("unix", p.socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", p.socketPath, err)
	}

	server := grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(server, p)

	p.listener = lis
	p.server = server

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		if err := server.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			p.log.Error("Device plugin server terminated", "err", err, "socketPath", p.socketPath)
		}
	}()

	if err := p.register(ctx); err != nil {
		_ = p.Stop()
		return err
	}

	return nil
}

func (p *ResourcePlugin) Stop() error {
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}

	if p.server != nil {
		p.server.Stop()
	}
	if p.listener != nil {
		_ = p.listener.Close()
	}
	p.wg.Wait()

	if err := os.Remove(p.socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

func (p *ResourcePlugin) UpdateDevices(devices map[string]NPUDevice) {
	p.mu.Lock()
	delta := diffDeviceInventories(p.devices, devices)
	p.devices = cloneDeviceMap(devices)
	current := clonePluginDevices(p.devices)
	healthy, unhealthy := healthCounts(p.devices)
	p.mu.Unlock()

	p.logInventoryDelta(delta, healthy, unhealthy)

	select {
	case p.updateCh <- current:
	default:
		select {
		case <-p.updateCh:
		default:
		}
		p.updateCh <- current
	}
}

// logInventoryDelta stays silent when a scan finds no change, and repeats the
// resulting counts on every record it does emit, so a single line answers both
// "what changed" and "what is allocatable now".
func (p *ResourcePlugin) logInventoryDelta(delta deviceInventoryDelta, healthy, unhealthy int) {
	if delta.isEmpty() {
		return
	}

	ctx := context.Background()
	log := p.log.With(
		"deviceCount", healthy+unhealthy,
		"healthyCount", healthy,
		"unhealthyCount", unhealthy,
	)

	for _, device := range delta.Added {
		log.Log(ctx, levelForHealth(device.Health), "Device appeared in inventory", deviceLogAttrs(device)...)
	}
	for _, id := range delta.Removed {
		log.Warn("Device disappeared from inventory; it is no longer allocatable", "device", id)
	}
	for _, change := range delta.Changed {
		log.Log(ctx, levelForHealth(change.Device.Health), "Device state changed",
			append(deviceLogAttrs(change.Device),
				"previousHealth", change.PreviousHealth,
				"previousStatus", change.PreviousStatus,
			)...,
		)
	}
}

func (p *ResourcePlugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return preferredAllocationOptions(), nil
}

func (p *ResourcePlugin) ListAndWatch(_ *pluginapi.Empty, stream pluginapi.DevicePlugin_ListAndWatchServer) error {
	devices := p.currentDevices()
	if err := stream.Send(&pluginapi.ListAndWatchResponse{Devices: devices}); err != nil {
		return err
	}
	p.log.Debug("Sent initial device list to kubelet", "deviceCount", len(devices))

	for {
		select {
		case <-p.stopCh:
			return nil
		case <-stream.Context().Done():
			return nil
		case devices := <-p.updateCh:
			if err := stream.Send(&pluginapi.ListAndWatchResponse{Devices: devices}); err != nil {
				return err
			}
			p.log.Debug("Sent updated device list to kubelet", "deviceCount", len(devices))
		}
	}
}

func (p *ResourcePlugin) Allocate(_ context.Context, request *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	response := &pluginapi.AllocateResponse{
		ContainerResponses: make([]*pluginapi.ContainerAllocateResponse, 0, len(request.ContainerRequests)),
	}

	for _, containerRequest := range request.ContainerRequests {
		start := time.Now()
		containerResponse, err := p.allocateContainer(containerRequest.DevicesIds)
		if err != nil {
			// durationMs separates a rejected request from one that burned the
			// kubelet's Allocate deadline waiting on a device node.
			p.log.Error("Container allocation failed",
				"err", err,
				"deviceIDs", containerRequest.DevicesIds,
				"durationMs", time.Since(start).Milliseconds(),
			)
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		response.ContainerResponses = append(response.ContainerResponses, containerResponse)
	}

	return response, nil
}

func (p *ResourcePlugin) GetPreferredAllocation(_ context.Context, request *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	response := &pluginapi.PreferredAllocationResponse{
		ContainerResponses: make([]*pluginapi.ContainerPreferredAllocationResponse, 0, len(request.ContainerRequests)),
	}

	for _, containerRequest := range request.ContainerRequests {
		deviceIDs, err := p.selectPreferredDeviceIDs(
			containerRequest.AvailableDeviceIDs,
			containerRequest.MustIncludeDeviceIDs,
			int(containerRequest.AllocationSize),
		)
		if err != nil {
			// Warn, not info: allocation still succeeds, but the devices
			// kubelet picks may straddle NUMA nodes or PCI bridges, and that
			// performance loss is otherwise silent. Request-driven, so the
			// volume is bounded by pod churn rather than repeating on a timer.
			p.log.Warn("Preferred allocation fallback to kubelet", "err", err, "allocationSize", containerRequest.AllocationSize)
			deviceIDs = nil
		} else {
			p.log.Debug("Selected preferred allocation", "allocationSize", containerRequest.AllocationSize, "deviceIDs", deviceIDs)
		}
		response.ContainerResponses = append(response.ContainerResponses, &pluginapi.ContainerPreferredAllocationResponse{
			DeviceIDs: deviceIDs,
		})
	}

	return response, nil
}

func (p *ResourcePlugin) allocateContainer(deviceIDs []string) (*pluginapi.ContainerAllocateResponse, error) {
	if len(deviceIDs) == 0 {
		return nil, fmt.Errorf("container request does not include any device IDs")
	}
	if p.cdi == nil {
		return nil, fmt.Errorf("CDI handler is not configured")
	}

	selected, err := p.selectedDevices(deviceIDs)
	if err != nil {
		return nil, err
	}

	busIDs := make([]string, 0, len(selected))
	for _, device := range selected {
		if device.Info.PCIBusID == "" {
			return nil, fmt.Errorf("device %q is missing PCIBusID", device.Info.Name)
		}
		busIDs = append(busIDs, device.Info.PCIBusID)
	}

	sort.Strings(busIDs)
	// Every durationMs below is measured from here, so one allocation's records
	// read as a timeline: RSD group setup versus waiting for device nodes.
	start := time.Now()
	p.log.Info("Starting container allocation",
		"deviceIDs", deviceIDs,
		"busIDs", busIDs,
	)
	hostRsdPath, err := p.rsdGroupFn(busIDs)
	if err != nil {
		return nil, fmt.Errorf("recreate RSD group for bus IDs %v: %w", busIDs, err)
	}
	p.log.Debug("Recreated RSD group",
		"busIDs", busIDs,
		"hostRsdPath", hostRsdPath,
		"durationMs", time.Since(start).Milliseconds(),
	)

	deviceSpecs, err := deviceSpecsForDevices(selected, hostRsdPath)
	if err != nil {
		return nil, err
	}

	annotations, err := p.cdi.RuntimeAnnotations()
	if err != nil {
		return nil, fmt.Errorf("create CDI runtime annotations: %w", err)
	}

	response := &pluginapi.ContainerAllocateResponse{
		Annotations: annotations,
		Devices:     deviceSpecs,
	}

	// Without durationMs a pod stuck in ContainerCreating — the common
	// device-plugin incident — shows up here only as "it eventually worked".
	p.log.Info("Completed container allocation",
		"deviceIDs", deviceIDs,
		"busIDs", busIDs,
		"hostRsdPath", hostRsdPath,
		"durationMs", time.Since(start).Milliseconds(),
	)

	return response, nil
}

func (p *ResourcePlugin) selectedDevices(deviceIDs []string) ([]NPUDevice, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	selected := make([]NPUDevice, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		device, ok := p.devices[id]
		if !ok {
			return nil, fmt.Errorf("resource %s does not manage device %q", p.resourceName, id)
		}
		selected = append(selected, device)
	}

	return selected, nil
}

func (p *ResourcePlugin) availableDevices(deviceIDs []string) (map[string]NPUDevice, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	available := make(map[string]NPUDevice, len(deviceIDs))
	for _, id := range deviceIDs {
		if _, exists := available[id]; exists {
			continue
		}
		device, ok := p.devices[id]
		if !ok {
			return nil, fmt.Errorf("resource %s does not manage device %q", p.resourceName, id)
		}
		available[id] = device
	}

	return available, nil
}

func (p *ResourcePlugin) currentDevices() []*pluginapi.Device {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return clonePluginDevices(p.devices)
}

func (p *ResourcePlugin) register(ctx context.Context) error {
	ctxDial, cancel := context.WithTimeout(ctx, registerTimeout)
	defer cancel()

	//nolint:staticcheck // grpc.DialContext remains the simplest way to block on a unix socket.
	conn, err := grpc.DialContext(
		ctxDial,
		p.kubeletSocket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", addr)
		}),
	)
	if err != nil {
		return fmt.Errorf("connect to kubelet socket %s: %w", p.kubeletSocket, err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			p.log.Warn("Failed to close kubelet registration connection", "err", err, "kubeletSocket", p.kubeletSocket)
		}
	}()

	client := pluginapi.NewRegistrationClient(conn)
	request := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     filepath.Base(p.socketPath),
		ResourceName: p.resourceName,
		Options:      preferredAllocationOptions(),
	}

	if _, err := client.Register(ctxDial, request); err != nil {
		return fmt.Errorf("register resource %s: %w", p.resourceName, err)
	}

	deviceIDs := sortedDeviceIDs(p.devices)
	healthy, unhealthy := healthCounts(p.devices)
	// No deviceIDs list here: each device gets its own record below, and
	// repeating the whole set turns one record into a second copy of them.
	p.log.Info("Registered device plugin with kubelet",
		"deviceCount", len(p.devices),
		"healthyCount", healthy,
		"unhealthyCount", unhealthy,
		"socketPath", p.socketPath,
	)
	for _, id := range deviceIDs {
		device := p.devices[id]
		p.log.Log(context.Background(), levelForHealth(device.Health), "Device exposed", deviceLogAttrs(device)...)
	}
	return nil
}

func preferredAllocationOptions() *pluginapi.DevicePluginOptions {
	return &pluginapi.DevicePluginOptions{
		GetPreferredAllocationAvailable: true,
	}
}

func cloneDeviceMap(devices map[string]NPUDevice) map[string]NPUDevice {
	cloned := make(map[string]NPUDevice, len(devices))
	for id, device := range devices {
		cloned[id] = NPUDevice{
			Info:       device.Info,
			Health:     device.Health,
			StatusName: device.StatusName,
		}
	}
	return cloned
}

func deviceSpecsForDevices(devices []NPUDevice, hostRsdPath string) ([]*pluginapi.DeviceSpec, error) {
	specs := make([]*pluginapi.DeviceSpec, 0, len(devices)+1)

	if hostRsdPath != "" {
		rsdSpec, err := newDeviceSpec("/dev/rsd0", hostRsdPath)
		if err != nil {
			return nil, fmt.Errorf("rsd device node: %w", err)
		}
		specs = append(specs, rsdSpec)
	}

	for _, device := range devices {
		devicePath := fmt.Sprintf("/dev/%s", device.Info.Name)
		rblnSpec, err := newDeviceSpec(devicePath, devicePath)
		if err != nil {
			return nil, fmt.Errorf("rbln device node: %w", err)
		}
		specs = append(specs, rblnSpec)
	}

	return specs, nil
}

func newDeviceSpec(containerPath, hostPath string) (*pluginapi.DeviceSpec, error) {
	if _, err := waitForDeviceNode(hostPath); err != nil {
		return nil, fmt.Errorf("stat device %q: %w", hostPath, err)
	}
	return &pluginapi.DeviceSpec{
		ContainerPath: containerPath,
		HostPath:      hostPath,
		Permissions:   "rw",
	}, nil
}

func waitForDeviceNode(hostPath string) (os.FileInfo, error) {
	deadline := time.Now().Add(deviceNodePollTimeout)
	for {
		fi, err := os.Stat(hostPath)
		if err == nil {
			return fi, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(deviceNodePollInterval)
	}
}
