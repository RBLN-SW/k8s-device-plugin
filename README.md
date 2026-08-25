# RBLN Device Plugin

`k8s-device-plugin` is a Kubernetes device plugin for Rebellions NPU devices.
It discovers locally available NPUs, exposes them through the kubelet device plugin
API, and prepares container runtime annotations for CDI-based integration.

The current implementation supports Rebellions device families exposed as:

- `rebellions.ai/ATOM`
- `rebellions.ai/REBEL`
- `rebellions.ai/npu` when generic resource mode is enabled

## Quick Start

### Option 1: Install Through RBLN NPU Operator

If your cluster is managed through the RBLN NPU Operator, install the operator first:

```bash
helm repo add rebellions https://rbln-sw.github.io/rbln-npu-operator
helm repo update

helm install --wait --generate-name \
  -n rbln-system --create-namespace \
  rebellions/rbln-npu-operator
```

### Option 2: Install This Device Plugin Chart Directly

1. Build and publish the image:

```bash
make -f deployments/container/Makefile build \
  IMAGE_NAME=<registry>/k8s-device-plugin \
  VERSION=<tag> \
  PUSH_ON_BUILD=true
```

2. Install the Helm chart from this repository:

```bash
helm upgrade --install rbln-device-plugin \
  ./deployments/helm/rbln-device-plugin \
  -n rbln-device-plugin \
  --create-namespace \
  --set image.repository=<registry>/k8s-device-plugin \
  --set image.tag=<tag>
```

3. Verify the rollout:

```bash
kubectl -n rbln-device-plugin get daemonset,pods
kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatable}{"\n"}{end}'
```

If generic resource mode is enabled, you should see `rebellions.ai/npu`.
Otherwise, allocatable resources are exposed as `rebellions.ai/ATOM` and/or
`rebellions.ai/REBEL` depending on installed hardware.

## Configuration

The binary can be configured with CLI flags or environment variables.

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `--cdi-root` | `CDI_ROOT` | `/var/run/cdi` | Directory used for CDI spec management |
| `--kubelet-device-plugin-path` | `KUBELET_DEVICE_PLUGIN_PATH` | `/var/lib/kubelet/device-plugins` | Kubelet device plugin socket directory |
| `--healthcheck-port` | `HEALTHCHECK_PORT` | `51515` | gRPC healthcheck port; set a negative value to disable it |
| `--use-generic-resource-name` | `USE_GENERIC_RESOURCE_NAME` | `false` | Expose `rebellions.ai/npu` instead of per-product resources |
| `--device-scan-interval` | `DEVICE_SCAN_INTERVAL` | `1m` | Polling interval for refreshing the device inventory |

Logging is configured separately, through environment variables only — see
[Logging](#logging).

## Logging

The plugin writes one structured stream to stdout: level-gated JSON by default,
including gRPC's own records. Nothing else uses stdout — usage errors, `--help`
and `--version` go to stderr — so a collector can parse every stdout line.
Two environment variables control it, exposed as the `logging.level` /
`logging.format` Helm values:

| Variable | Values | Default |
| --- | --- | --- |
| `RBLN_DEVICE_PLUGIN_LOG_LEVEL` | `error`, `warning` (or `warn`), `info`, `debug` | `info` |
| `RBLN_DEVICE_PLUGIN_LOG_FORMAT` | `json`, `text` | `json` |

There are deliberately no CLI flags: the logger is installed before flags are
parsed, so even a usage error is reported through it. Invalid values fall back to
the defaults with a warning instead of failing startup.

> When the plugin runs as an operand of the NPU operator, these variables must be
> set on the operator-managed DaemonSet — this chart's values do not reach it.

Every record carries `ts` (RFC3339Nano), lowercase `level`, `msg`, and the keys
of the event; errors are always under `err`. Records produced by gRPC itself
carry `component=grpc`, and `caller` is added at `debug`.

```json
{"ts":"2026-08-21T09:14:02.113Z","level":"warn","msg":"Device state changed","resourceName":"rebellions.ai/npu","device":"rbln3","health":"Unhealthy","status":"FAULT","previousHealth":"Healthy","previousStatus":"READY","deviceCount":8,"healthyCount":7,"unhealthyCount":1}
```

### What the default stream tells you

At `info` the stream is a narrative of state, not a heartbeat — a scan that finds
nothing new logs nothing:

- **Lifecycle** — `Starting rbln-device-plugin` (with version and the resolved
  configuration), `Registered device plugin with kubelet` plus one
  `Device exposed` per device, then `Shutdown signal received` (naming the
  `signal`, so a kubelet drain is distinguishable from a crash-adjacent
  `SIGQUIT`) and `Stopped rbln-device-plugin`.
- **Inventory changes** — `Device appeared in inventory`,
  `Device disappeared from inventory` (warn), and `Device state changed`. Each
  carries the resulting `deviceCount` / `healthyCount` / `unhealthyCount`, so one
  record answers both what changed and what is allocatable now.
- **Allocation** — `Starting container allocation` and
  `Completed container allocation`, both with `deviceIDs` and `busIDs`;
  `Container allocation failed` (error) when a request is rejected. Both
  terminal records carry `durationMs`, because this handler is what stalls a pod
  in `ContainerCreating`.
- **Degraded placement** — `Preferred allocation fallback to kubelet` (warn):
  allocation still succeeds, but topology-aware selection did not run, so the
  chosen devices may straddle NUMA nodes or PCI bridges.
- **Recovery events** — `Detected kubelet socket recreation; restarting device
  plugins`, `Stopped device plugin for absent resource`,
  `Device discovery failed; reporting zero devices until it recovers` (error,
  repeated per scan while the node has no usable devices).

Levels follow one rule: `error` means a device or request is unusable now,
`warn` means the plugin handled something abnormal, `info` is lifecycle and
state changes, and `debug` adds per-request flow (device list pushes, RSD group
recreation, preferred allocation, and gRPC internals). Any record describing a
device is raised to `warn` when that device is unhealthy — including one that is
already faulted the first time it is seen — so alerting on `warn` cannot miss
unusable hardware.

Because a steady node logs nothing, `debug` also carries
`Reconciled device inventory` once per `--device-scan-interval`: it is how you
confirm the scan loop is alive and how long a scan takes.

## License

This project is licensed under the Apache License 2.0. See
[`LICENSE`](LICENSE) for details.
