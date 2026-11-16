# Diagnose a Pod that cannot use an NPU

Use this guide for Pods requesting `rebellions.ai/*` through resource limits.
For ResourceClaims, see the [DRA Driver documentation](https://docs.rbln.ai/latest/software/system_management/kubernetes/npu_dra_driver.html).

## 1. Read the workload events

```bash
kubectl describe pod <pod-name> -n <workload-namespace>
kubectl describe node <npu-node>
```

For an unscheduled Pod, select a candidate NPU node. Compare the requested resource
name and count with Capacity, Allocatable, and existing requests. Allocatable
includes devices already requested by other Pods. Also check selectors and taints.

| Symptom | Check |
| --- | --- |
| `Insufficient rebellions.ai/npu` | Unpartitioned-device capacity and existing requests; partitioned devices use `npu-vf1` or `npu-vf4`, and both resource types can coexist on a node |
| No NPU resources | Driver readiness, plugin scheduling, and kubelet registration |
| `ContainerCreating` or allocation errors | Plugin logs for device health, RSD group creation, and pre-start failures |
| Pod runs but libraries or tools are missing | CDI-enabled runtime and the Container Toolkit's runtime spec |

## 2. Read the plugin logs on that node

```bash
kubectl get pods -n rbln-system -o wide
kubectl logs <device-plugin-pod> -n rbln-system --all-containers --tail=200
```

Use `rbln-device-plugin` instead of `rbln-system` for the standalone guide's
namespace. Select the plugin Pod on the workload's node. If needed,
[enable debug logging](../reference/configuration.md#logging-and-allocation-traces).

An unhealthy device is withheld from scheduling until the driver reports READY.
For RSD failures, inspect the reported device IDs and driver error; allocated
NPUs must have a usable RSD group. Do not manually change groups used by live Pods.

`Preferred allocation fallback to kubelet` means allocation can continue, but
topology-aware selection did not run. OTLP exporter errors affect tracing rather
than NPU availability.

## 3. Verify recovery

Run the [one-NPU example](../../examples/single-pod-single-npu.md) after resolving
the reported problem. Expect a Ready Pod with its allocated `/dev/rbln*` device
and `/dev/rsd0`. If a partition or workload-mode change is still in progress,
wait for it to finish and confirm that the node advertises the requested resource
before creating another workload.

[Back to the guides](../../README.md#guides)
