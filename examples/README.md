# Usage Examples

This directory contains usage examples for the device plugin.

A default installation uses the generic resource name `rebellions.ai/npu`.

When generic resource mode is disabled
(`--use-generic-resource-name=false`), resource names depend on the
installed hardware:

- `rebellions.ai/ATOM`
- `rebellions.ai/REBEL`

On SR-IOV partitioned nodes, use `rebellions.ai/npu-vf1` or
`rebellions.ai/npu-vf4` depending on the partition mode.

## Example Scenarios

- [Single pod requesting two NPUs](single-pod-double-npu.md)
- [Two pods requesting one NPU each](two-pods-one-npu-each.md)
- [Single pod requesting SR-IOV VFs (vf-4)](single-pod-npu-vf4.md)
