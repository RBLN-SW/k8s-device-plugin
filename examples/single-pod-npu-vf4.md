# Single Pod Requesting SR-IOV VFs (4 VFs per PF)

A pod requesting one virtual function (VF) from NPUs configured with four VFs
per physical function (PF).

Prerequisite: configure the host NPUs with four VFs per PF and make the VFs
available through the native NPU driver. The device plugin discovers these
VFs and advertises them as `rebellions.ai/npu-vf4`. With one VF per PF, it
advertises `rebellions.ai/npu-vf1` instead.

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

The container receives the allocated VF device nodes (e.g. `/dev/rbln0-0`)
and a dedicated RSD group node mounted as `/dev/rsd0`, just like PF allocations.

## Cleanup

```bash
kubectl delete namespace npu-example-vf4
```
