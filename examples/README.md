# Usage Examples

This directory contains usage examples for the device plugin.

Non-partitioned NPUs always use the fixed resource name `rebellions.ai/npu`.

On SR-IOV partitioned nodes, use `rebellions.ai/npu-vf1` or
`rebellions.ai/npu-vf4` depending on the partition mode.

## Example Scenarios

- [Single pod requesting two NPUs](single-pod-double-npu.md)
- [Two pods requesting one NPU each](two-pods-one-npu-each.md)
- [Single pod requesting SR-IOV VFs (vf-4)](single-pod-npu-vf4.md)
