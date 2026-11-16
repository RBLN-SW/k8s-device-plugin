# Device Plugin examples

Start with one NPU, then choose the allocation pattern you need.

## Before you start

[Install the Device Plugin and Container Toolkit](https://docs.rbln.ai/latest/software/system_management/kubernetes/install_npu_operator.html)
and confirm that the node advertises the resource used by your example.
Use examples from the same release as your deployment.

| Goal | Guide |
| --- | --- |
| Run a first NPU workload | [One Pod, one NPU](single-pod-single-npu.md) |
| Allocate multiple NPUs to one workload | [One Pod, two NPUs](single-pod-double-npu.md) |
| Give each workload its own NPU | [Two Pods, one NPU each](two-pods-one-npu-each.md) |
| Use a partitioned NPU | [One Pod, one VF](single-pod-npu-vf4.md) |

Whole NPUs use `rebellions.ai/npu`. Partitioned devices use
`rebellions.ai/npu-vf1` or `rebellions.ai/npu-vf4`.
Each guide includes cleanup for the namespace it creates.

[Back to the README](../README.md)
