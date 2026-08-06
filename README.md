# RBLN Device Plugin

`k8s-device-plugin` is a Kubernetes device plugin for Rebellions NPU devices.
It discovers locally available NPUs, exposes them through the kubelet device plugin
API, and prepares container runtime annotations for CDI-based integration.

The current implementation supports Rebellions device families exposed as:

- `rebellions.ai/npu` (default, generic resource mode)
- `rebellions.ai/ATOM` and `rebellions.ai/REBEL` when generic resource mode
  is disabled
- `rebellions.ai/npu-vf1` / `rebellions.ai/npu-vf4` for SR-IOV virtual
  functions on partitioned REBEL nodes (see below)

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

By default you should see `rebellions.ai/npu`. If generic resource mode is
disabled, allocatable resources are exposed as `rebellions.ai/ATOM` and/or
`rebellions.ai/REBEL` depending on installed hardware.

## Configuration

The binary can be configured with CLI flags or environment variables.

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `--cdi-root` | `CDI_ROOT` | `/var/run/cdi` | Directory used for CDI spec management |
| `--kubelet-device-plugin-path` | `KUBELET_DEVICE_PLUGIN_PATH` | `/var/lib/kubelet/device-plugins` | Kubelet device plugin socket directory |
| `--healthcheck-port` | `HEALTHCHECK_PORT` | `51515` | gRPC healthcheck port; set a negative value to disable it |
| `--use-generic-resource-name` | `USE_GENERIC_RESOURCE_NAME` | `true` | Expose `rebellions.ai/npu` instead of per-product resources |
| `--device-scan-interval` | `DEVICE_SCAN_INTERVAL` | `1m` | Polling interval for refreshing the device inventory |

## SR-IOV Partitioned Nodes (REBEL)

REBEL NPUs can be partitioned into SR-IOV virtual functions (supported
partition modes: `vf-1` and `vf-4`). The plugin detects VFs directly from
sysfs — no flag or chart value is required — and advertises them as a
dedicated resource derived from the partition mode:

| Partition mode | Advertised resource | Devices per PF |
| --- | --- | --- |
| `vf-1` | `rebellions.ai/npu-vf1` | 1 (all 4 chiplets) |
| `vf-4` | `rebellions.ai/npu-vf4` | 4 (1 chiplet each) |

Behavior on a partitioned node:

- VF resources are always named `rebellions.ai/npu-vf<N>`, regardless of
  `--use-generic-resource-name`.
- A PF that hosts VFs is not advertised: in the current driver generation it
  is removed from the compute topology while SR-IOV is enabled, so exposing
  it would double-count compute capacity.
- Non-partitioned PFs on the same node keep their usual resource names, so
  mixed nodes advertise both (e.g. `rebellions.ai/npu-vf4` and
  `rebellions.ai/npu`).
- PFs with an unsupported VF count are excluded from advertisement and
  logged as errors.
- VF allocations do not create an RSD group and do not mount `/dev/rsd0`;
  each container receives its VF device nodes (e.g. `/dev/rbln0-0`) plus the
  usual CDI runtime annotation.

Prerequisite: partitioning itself is managed outside this plugin. When the
plugin is deployed through the RBLN NPU Operator, its init container gates
startup until the node's partition state is ready
(`/run/rbln/validations/partition-ready`), so VFs already exist by the time
the plugin scans devices.

See [`examples/single-pod-npu-vf4.md`](examples/single-pod-npu-vf4.md) for a
pod requesting VFs.

## License

This project is licensed under the Apache License 2.0. See
[`LICENSE`](LICENSE) for details.
