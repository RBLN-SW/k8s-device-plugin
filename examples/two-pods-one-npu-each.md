# Two Pods Requesting One NPU Each

Two pods each requesting one NPU.

## Apply

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: npu-example-two-pods
---
apiVersion: v1
kind: Pod
metadata:
  namespace: npu-example-two-pods
  name: pod0
spec:
  containers:
  - name: ctr0
    image: ubuntu:22.04
    command: ["bash", "-c"]
    args: ["trap 'exit 0' TERM; sleep 9999 & wait"]
    resources:
      limits:
        rebellions.ai/npu: 1
      requests:
        rebellions.ai/npu: 1
---
apiVersion: v1
kind: Pod
metadata:
  namespace: npu-example-two-pods
  name: pod1
spec:
  containers:
  - name: ctr0
    image: ubuntu:22.04
    command: ["bash", "-c"]
    args: ["trap 'exit 0' TERM; sleep 9999 & wait"]
    resources:
      limits:
        rebellions.ai/npu: 1
      requests:
        rebellions.ai/npu: 1
EOF
```

## Verify

```bash
kubectl -n npu-example-two-pods get pod
kubectl -n npu-example-two-pods describe pod pod0
kubectl -n npu-example-two-pods describe pod pod1
```

## Cleanup

```bash
kubectl delete namespace npu-example-two-pods
```
