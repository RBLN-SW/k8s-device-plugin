# Run a Pod with one NPU

Request a whole NPU and confirm that it is available inside a container.

## Before you start

Install the Device Plugin and Container Toolkit using the
[NPU Operator guide](https://docs.rbln.ai/latest/software/system_management/kubernetes/install_npu_operator.html).
The cluster needs an available whole-NPU resource named `rebellions.ai/npu`.
Use a separate test namespace; this example creates `rbln-device-plugin-example`.

## 1. Create the Pod

From the repository root:

```bash
kubectl apply -f examples/manifests/single-pod-single-npu.yaml
```

The [manifest](manifests/single-pod-single-npu.yaml) requests one NPU through the
container's resource limits.

## 2. Verify

```bash
kubectl wait -n rbln-device-plugin-example pod/npu-pod --for=condition=Ready --timeout=5m
kubectl exec -n rbln-device-plugin-example npu-pod -- sh -c 'ls /dev/rbln* /dev/rsd*'
kubectl exec -n rbln-device-plugin-example npu-pod -- rbln-smi -L
```

Expect the Pod to become Ready, `ls` to list its allocated NPU and RSD group
device nodes, and `rbln-smi -L` to report one NPU. Device numbers depend on the
allocation.

If the Pod stays Pending, inspect scheduling events and node capacity:

```bash
kubectl describe pod npu-pod -n rbln-device-plugin-example
kubectl describe node <npu-node>
```

## 3. Clean up

Remove the namespace created for this example:

```bash
kubectl delete namespace rbln-device-plugin-example
```

Next: [request two NPUs](single-pod-double-npu.md) or
[request an SR-IOV VF](single-pod-npu-vf4.md).
