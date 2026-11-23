# Deploy the component chart

New releases publish the component image only. The chart remains available in the
source tree at each Git tag. For an OCI chart, install the NPU Operator.

Use this guide when you manage this component separately from the NPU Operator.

## Before you start

- Prepared NPU nodes with a compatible driver, Container Toolkit, and CDI-enabled runtime.
- Helm and `kubectl` configured for the target cluster.
- A checkout of the release you intend to deploy.

The NPU Operator must not also deploy the Device Plugin on these nodes. Use a
single allocator for each device; DRA must be disabled for the same devices.
Plan any allocator change before installing this chart.

## 1. Choose the image and settings

Select the image tag for your release and review the chart's
[values](../../deployments/helm/k8s-device-plugin-chart/values.yaml).
Confirm that its node selector and tolerations match the prepared NPU nodes.
Use a values file for any site-specific overrides.

## 2. Install

Run from the repository root. Replace the image tag before running:

```bash
export COMPONENT_IMAGE_TAG='<image-tag>'
helm upgrade --install rbln-device-plugin \
  ./deployments/helm/k8s-device-plugin-chart \
  --namespace rbln-device-plugin --create-namespace \
  --set-string image.repository=rebellions/k8s-device-plugin \
  --set-string image.tag="$COMPONENT_IMAGE_TAG" \
  --wait --timeout 5m
```

Add `-f <values-file>` if you prepared custom settings.

## 3. Verify

```bash
kubectl get daemonsets,pods -n rbln-device-plugin
kubectl describe node <npu-node>
```

Expect Ready Pods on the selected NPU nodes and the expected device inventory.
Then [run a Pod with one NPU](../../examples/single-pod-single-npu.md).

## Remove this deployment

Stop workloads using this allocator and release their device allocations first.
Then remove only this Helm release:

```bash
helm uninstall rbln-device-plugin -n rbln-device-plugin
```

Return to the [component guides](../../README.md#guides).
