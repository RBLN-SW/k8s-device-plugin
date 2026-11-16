# RBLN Device Plugin

**Request Rebellions NPUs from Kubernetes Pods.**

[Get started](#get-started) · [Guides](#guides) · [Examples](examples/README.md) · [Releases](https://github.com/RBLN-SW/k8s-device-plugin/releases)

The device plugin makes NPUs available as Kubernetes resources. A Pod requests
an NPU through `resources.limits`, and the container runtime makes the allocated
device available inside the container.

## Get started

1. [Install the NPU Operator](https://docs.rbln.ai/latest/software/system_management/kubernetes/install_npu_operator.html)
   with the Device Plugin and Container Toolkit enabled.
2. [Run a Pod with one NPU](examples/single-pod-single-npu.md).
3. Confirm that the Pod is Ready and can access its NPU device.

If the operator already manages the plugin, use that deployment for the examples.
For a separate component deployment, follow the [standalone chart guide](docs/guides/deploy-standalone.md).

## Request an NPU

Add this fragment to a Pod's container specification:

```yaml
resources:
  limits:
    rebellions.ai/npu: 1
```

The [complete example](examples/single-pod-single-npu.md) includes setup,
verification, and cleanup. Partitioned devices expose `rebellions.ai/npu-vf1` or
`rebellions.ai/npu-vf4`. Unpartitioned devices on the same node continue to expose
`rebellions.ai/npu`.

## Guides

| I want to… | Guide |
| --- | --- |
| Run one Pod with two NPUs | [Request two NPUs](examples/single-pod-double-npu.md) |
| Give each of two Pods an NPU | [Run two Pods](examples/two-pods-one-npu-each.md) |
| Request an SR-IOV virtual function | [Use partitioned NPUs](examples/single-pod-npu-vf4.md) |
| Schedule by NPU family | [Choose a node](https://docs.rbln.ai/latest/software/system_management/kubernetes/tutorials/find_and_schedule.html) |
| Configure runtime integration or allocation tracing | [Configure the plugin](https://docs.rbln.ai/latest/software/system_management/kubernetes/device_plugin.html) |
| Investigate a Pending or stuck Pod | [Diagnose allocation](docs/guides/troubleshoot-allocation.md) |
| Change logs or component settings | [Configuration reference](docs/reference/configuration.md) |

For allocation through ResourceClaims, see the [RBLN DRA Driver](https://github.com/RBLN-SW/rbln-k8s-dra-driver).
The operator supports DRA and the Device Plugin as alternative allocation methods.

## Development

See [Contributing](CONTRIBUTING.md) to build, test, and validate a change.
Use docs and examples from the Git tag matching your deployed
[release](https://github.com/RBLN-SW/k8s-device-plugin/releases).

## Support

Report bugs and feature requests in [GitHub issues](https://github.com/RBLN-SW/k8s-device-plugin/issues).

[License](LICENSE)
