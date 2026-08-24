package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Manager struct {
	mu      sync.Mutex
	config  *Config
	cdi     *CDIHandler
	health  *healthServer
	plugins map[string]*ResourcePlugin
}

func NewManager(config *Config) (*Manager, error) {
	cdi, err := NewCDIHandler(config.flags.cdiRoot)
	if err != nil {
		return nil, err
	}
	if err := cdi.Initialize(); err != nil {
		return nil, err
	}

	health, err := startHealthcheck(config.flags.healthcheckPort)
	if err != nil {
		return nil, err
	}

	return &Manager{
		config:  config,
		cdi:     cdi,
		health:  health,
		plugins: make(map[string]*ResourcePlugin),
	}, nil
}

func (m *Manager) Run(ctx context.Context) error {
	if err := m.reconcile(ctx); err != nil {
		return err
	}
	if m.health != nil {
		m.health.SetServing(true)
	}

	deviceTicker := time.NewTicker(m.config.flags.deviceScanInterval)
	defer deviceTicker.Stop()

	kubeletWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create kubelet socket watcher: %w", err)
	}
	defer func() {
		if err := kubeletWatcher.Close(); err != nil {
			slog.Warn("Failed to close kubelet socket watcher", "err", err)
		}
	}()

	if err := kubeletWatcher.Add(m.config.flags.kubeletDevicePluginPath); err != nil {
		return fmt.Errorf("watch kubelet device-plugin directory %s: %w", m.config.flags.kubeletDevicePluginPath, err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deviceTicker.C:
			if err := m.reconcile(ctx); err != nil {
				slog.Error("Device reconciliation failed", "err", err)
			}
		case event, ok := <-kubeletWatcher.Events:
			if !ok {
				return fmt.Errorf("kubelet socket watcher closed unexpectedly")
			}
			if !isKubeletRestartEvent(m.config.KubeletSocketPath(), event) {
				continue
			}

			slog.Info("Detected kubelet socket recreation; restarting device plugins",
				"path", event.Name,
				"op", event.Op.String(),
			)
			if err := m.restart(ctx); err != nil {
				return err
			}
		case err, ok := <-kubeletWatcher.Errors:
			if !ok {
				return fmt.Errorf("kubelet socket watcher closed unexpectedly")
			}
			slog.Error("Kubelet socket watcher error", "err", err)
		}
	}
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.health != nil {
		m.health.SetServing(false)
		m.health.Stop()
	}

	for resourceName, plugin := range m.plugins {
		if err := plugin.Stop(); err != nil {
			slog.Error("Failed to stop device plugin", "err", err, "resourceName", resourceName)
		}
	}
	slog.Info("Stopped rbln-device-plugin", "resourceCount", len(m.plugins))
	m.plugins = make(map[string]*ResourcePlugin)
}

func (m *Manager) restart(ctx context.Context) error {
	m.mu.Lock()
	for resourceName, plugin := range m.plugins {
		if err := plugin.Stop(); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("stop device plugin %s: %w", resourceName, err)
		}
	}
	m.plugins = make(map[string]*ResourcePlugin)
	if err := m.cdi.Initialize(); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	return m.reconcile(ctx)
}

func (m *Manager) reconcile(ctx context.Context) error {
	start := time.Now()
	groups := discoverDeviceGroups(ctx, m.config.flags.useGenericResourceName)
	discovered := 0
	for _, group := range groups {
		discovered += len(group.Devices)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for resourceName, plugin := range m.plugins {
		group, ok := groups[resourceName]
		if !ok {
			if err := plugin.Stop(); err != nil {
				return fmt.Errorf("stop device plugin %s: %w", resourceName, err)
			}
			delete(m.plugins, resourceName)
			slog.Info("Stopped device plugin for absent resource", "resourceName", resourceName)
			continue
		}
		plugin.UpdateDevices(group.Devices)
		delete(groups, resourceName)
	}

	for resourceName, group := range groups {
		socketPath := filepath.Join(m.config.flags.kubeletDevicePluginPath, socketNameForResource(resourceName))
		plugin := NewResourcePlugin(resourceName, socketPath, m.config.KubeletSocketPath(), m.cdi, group.Devices)
		if err := plugin.Start(ctx); err != nil {
			return fmt.Errorf("start device plugin %s: %w", resourceName, err)
		}
		m.plugins[resourceName] = plugin
	}

	// The info stream only reports change, so it cannot answer "is the scan loop
	// still running, and how long does a scan take" — that is what this is for.
	slog.Debug("Reconciled device inventory",
		"resourceCount", len(m.plugins),
		"deviceCount", discovered,
		"durationMs", time.Since(start).Milliseconds(),
	)

	return nil
}

func socketNameForResource(resourceName string) string {
	return fmt.Sprintf("rbln-device-plugin-%s.sock", resourceSlug(resourceName))
}

func isKubeletRestartEvent(kubeletSocketPath string, event fsnotify.Event) bool {
	if filepath.Clean(event.Name) != filepath.Clean(kubeletSocketPath) {
		return false
	}

	return event.Has(fsnotify.Create)
}
