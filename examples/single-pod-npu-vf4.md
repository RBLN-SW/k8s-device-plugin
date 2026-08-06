# Single Pod Requesting SR-IOV VFs (vf-4)

A pod requesting a single VF on a node partitioned in SR-IOV `vf-4` mode.

Prerequisite: the node must be partitioned in `vf-4` mode (performed by the
RBLN NPU Operator's partition-manager). Partitioned nodes expose the
resource as `rebellions.ai/npu-vf4` (or `rebellions.ai/npu-vf1` in `vf-1`
mode).

## Apply

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: npu-example-vf4
---
apiVersion: v1
kind: Pod
metadata:
  namespace: npu-example-vf4
  name: pod0
spec:
  containers:
  - name: ctr0
    image: ubuntu:22.04
    command: ["bash", "-c"]
    args: ["trap 'exit 0' TERM; sleep 9999 & wait"]
    resources:
      limits:
        rebellions.ai/npu-vf4: 1
      requests:
        rebellions.ai/npu-vf4: 1
EOF
```

## Verify

```bash
kubectl -n npu-example-vf4 get pod
kubectl -n npu-example-vf4 describe pod pod0
```

The container receives only the allocated VF device nodes
(e.g. `/dev/rbln0-0`); unlike PF allocations, `/dev/rsd0` is not mounted.

## Cleanup

```bash
kubectl delete namespace npu-example-vf4
```
