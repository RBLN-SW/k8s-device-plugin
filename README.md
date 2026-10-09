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
helm upgrade --install k8s-device-plugin \
  ./deployments/helm/k8s-device-plugin-chart \
  -n k8s-device-plugin \
  --create-namespace \
  --set image.repository=<registry>/k8s-device-plugin \
  --set image.tag=<tag>
```

3. Verify the rollout:

```bash
kubectl -n k8s-device-plugin get daemonset,pods
kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatable}{"\n"}{end}'
```

If generic resource mode is enabled, you should see `rebellions.ai/npu`.
Otherwise, allocatable resources are exposed as `rebellions.ai/ATOM` and/or
`rebellions.ai/REBEL` depending on installed hardware.

The chart is named `k8s-device-plugin-chart`; release workflows publish it to
`<registry>/k8s-device-plugin-chart:<chart-version>`. The container image uses
`<registry>/k8s-device-plugin:<image-tag>`.

## Configuration

The binary can be configured with CLI flags or environment variables.

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `--cdi-root` | `CDI_ROOT` | `/var/run/cdi` | Directory used for CDI spec management |
| `--kubelet-device-plugin-path` | `KUBELET_DEVICE_PLUGIN_PATH` | `/var/lib/kubelet/device-plugins` | Kubelet device plugin socket directory |
| `--healthcheck-port` | `HEALTHCHECK_PORT` | `51515` | gRPC healthcheck port; set a negative value to disable it |
| `--use-generic-resource-name` | `USE_GENERIC_RESOURCE_NAME` | `false` | Expose `rebellions.ai/npu` instead of per-product resources |
| `--device-scan-interval` | `DEVICE_SCAN_INTERVAL` | `1m` | Polling interval for refreshing the device inventory |
| `--otlp-endpoint` | `OTEL_EXPORTER_OTLP_ENDPOINT` | (empty) | OTLP gRPC endpoint to export allocation traces to; leave empty to disable tracing |

## Preferred device allocation

The plugin reads each candidate's `/sys/bus/pci/devices/<BDF>` symlink and builds
a forest of PCI roots, shared upstream bridges, and NPU endpoints. This works
for CA22, CA25, and CR13 without product-specific bridge depths. Placement does
not call `rbln-smi --topo` or read the driver's RSD-filtered `topology` attribute,
so splitting devices into RSD groups does not hide their PCI relationships.
Device discovery and RSD group creation still use the existing library.

Selection preserves kubelet's mandatory devices and compares complete device
combinations, including partial selections within the same SID. The policy is
lexicographic; lower scores win in this order:

1. Number of selected devices with unknown NUMA, then number of distinct known
   NUMA nodes. NUMA is evaluated per selected device, not from the first device
   in a SID group. Missing, invalid, and negative NUMA values are unknown.
2. Number of distinct SID groups. A valid shared SID is evidence of a shared
   card. Empty, `N/A`, `unknown`, and all-zero SIDs are treated as separate
   devices, not as one shared card.
3. Number of devices with unreadable PCI paths, then number of device pairs
   under separate PCI roots. Separate roots have no known common ancestor;
   the plugin does not invent an inter-root hop count.
4. Maximum LCA distance among connected pairs, then the sum of those distances.
   Mandatory devices participate in both metrics. For endpoints A and B,
   `distance = depth(A) + depth(B) - 2 * depth(LCA(A, B))`.
5. Lexicographic device ID order for deterministic ties.

NUMA locality takes priority even when keeping devices on one NUMA node uses
more cards. For example, four available devices on three local cards beat
a two-card combination spanning NUMA nodes. SID packing then minimizes the
cards used, and PCI distance minimizes the worst path before the total path
length. Group fullness or a fixed ID prefix cannot override these priorities.
Unknown PCI information only affects that device; valid
paths between the other selected devices remain usable. If all topology
metadata is absent, allocation still returns the requested number of available
devices, including the mandatory ones, with stable ID ordering.

For example, consider this PCI tree (all devices initially have distinct SIDs
and NUMA node 0):

```text
PCI root A
├── Root port 0
│   └── Switch
│       ├── Bridge A
│       │   ├── rbln0
│       │   └── rbln2
│       └── Bridge B
│           ├── rbln1
│           └── rbln3
└── Root port 1
    └── rbln4
PCI root B
└── Root port
    └── rbln5
rbln6: PCI path unavailable
```

The distances are `d(0,2)=2`, `d(0,1)=4`, and `d(0,4)=6`.
`rbln0` and `rbln5` belong to separate roots, so their distance is not known.

| Request / changed information | Preferred devices | Reason |
| --- | --- | --- |
| Choose 2 | `rbln0, rbln2` | Closest pair; ID order breaks the tie with `rbln1, rbln3` |
| Choose 2; only 0/2/4 available; 0/4 share a SID | `rbln0, rbln4` | Keep one shared card despite the longer visible PCI path |
| Include 0, choose 2 from 0/1/2; 2 is on NUMA 1 | `rbln0, rbln1` | Same NUMA takes precedence over the shorter 0–2 PCI path |
| Choose 2 from 0/1/2 sharing one SID | `rbln0, rbln2` | Compare subsets within the SID instead of taking the first two IDs |
| Include 6, choose 3 | `rbln6, rbln0, rbln2` | Keep the required device with missing PCI data and optimize the remaining known pair |
| Include 0, choose 2 from 0/4/5 | `rbln0, rbln4` | A path under one PCI root is preferable to an unmeasured inter-root path |
| Choose 2 with all SID/NUMA/PCI information missing | `rbln0, rbln1` | Deterministic fallback |

The search uses admissible bounds to skip combinations that cannot improve the
score; a tree dynamic program bounds the remaining PCI cost without enumerating
every subset. It does not greedily choose one neighbor at a time. These examples and
an independent exhaustive comparison are covered by `pci_topology_test.go`.
In a VM, the tree is the guest's PCI hierarchy. The metric is a locality
heuristic, not a measurement of physical host distance, bandwidth, or P2P support.

## Observability

The plugin can export NPU allocation traces via OpenTelemetry. When an OTLP
gRPC endpoint is configured, each kubelet `Allocate` call produces an
`Allocate` span with a child `allocateContainer` span per container, carrying
the assigned NPU IDs, PCI bus IDs, and RSD group path. Spans include the service
name (`service.name`) and the node name (`k8s.node.name`).

Tracing is disabled by default and is strictly best-effort: if the endpoint is
empty, tracing is a no-op, and if exporter setup fails (e.g. a malformed
endpoint), the plugin logs a warning and keeps running without tracing rather
than aborting NPU scheduling on the node.

With the Helm chart, set the endpoint via:

```bash
helm upgrade --install k8s-device-plugin \
  ./deployments/helm/k8s-device-plugin-chart \
  -n k8s-device-plugin \
  --set devicePlugin.otlpEndpoint=<collector-host>:4317
```

An endpoint given as `host:port` uses an insecure gRPC connection; pass a full
URL (e.g. `https://collector:4317`) to use TLS.

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

- **Lifecycle** — startup (with version and the resolved
  configuration), `Registered device plugin with kubelet` plus one
  `Device exposed` per device, then `Shutdown signal received` (naming the
  `signal`, so a kubelet drain is distinguishable from a crash-adjacent
  `SIGQUIT`) and shutdown.
- **Inventory changes** — `Device appeared in inventory`,
  `Device disappeared from inventory` (warn), and `Device state changed`. Each
  carries the resulting `deviceCount` / `healthyCount` / `unhealthyCount`, so one
  record answers both what changed and what is allocatable now.
- **Allocation** — `Starting container allocation` and
  `Completed container allocation`, both with `deviceIDs` and `busIDs`;
  `Container allocation failed` (error) when a request is rejected. Both
  terminal records carry `durationMs`, because this handler is what stalls a pod
  in `ContainerCreating`.
- **Tracing** — `Distributed tracing enabled` or
  `Distributed tracing disabled; no OTLP endpoint configured` at startup,
  `Tracing setup failed; continuing without distributed tracing` (warn) when the
  exporter cannot be built, and `OpenTelemetry SDK error` (warn,
  `component=otel`) while the collector is unreachable. Nothing tracing reports
  rises above warn: it is best-effort, so none of it makes a device or an
  allocation unusable.
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
recreation, preferred allocation, and gRPC / OpenTelemetry SDK internals). Any record describing a
device is raised to `warn` when that device is unhealthy — including one that is
already faulted the first time it is seen — so alerting on `warn` cannot miss
unusable hardware.

Because a steady node logs nothing, `debug` also carries
`Reconciled device inventory` once per `--device-scan-interval`: it is how you
confirm the scan loop is alive and how long a scan takes.

## License

This project is licensed under the Apache License 2.0. See
[`LICENSE`](LICENSE) for details.
