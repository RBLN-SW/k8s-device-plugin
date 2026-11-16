# Device Plugin settings

For an operator deployment, use the operator's values. For a standalone chart,
use this repository's [values.yaml](../../deployments/helm/k8s-device-plugin-chart/values.yaml).
The two charts use different paths for some settings.

## Logging and allocation traces

| Setting | Operator values | Standalone chart values |
| --- | --- | --- |
| Log level | `devicePlugin.logging.level` | `logging.level` |
| Log format | `devicePlugin.logging.format` | `logging.format` |
| OTLP endpoint | `devicePlugin.otlpEndpoint` | `devicePlugin.otlpEndpoint` |

For example, merge this into operator values:

```yaml
devicePlugin:
  logging:
    level: debug
    format: json
```

Apply through the [operator upgrade procedure](https://docs.rbln.ai/latest/software/system_management/kubernetes/upgrade_npu_operator.html).
For a standalone deployment, use the [component chart procedure](../guides/deploy-standalone.md)
with `logging.level: debug` at the top level of your values file. Read the updated
Pod's logs, then restore `info` after diagnosis.

Binary logging uses `RBLN_DEVICE_PLUGIN_LOG_LEVEL` (`info` by default; accepts
`error`, `warning`/`warn`, `info`, `debug`) and `RBLN_DEVICE_PLUGIN_LOG_FORMAT`
(`json` by default; also accepts `text`). Invalid values fall back with a warning.
Logs go to stdout; debug output adds per-request detail and the caller.

Tracing is disabled without an OTLP endpoint. A `host:port` endpoint uses plaintext;
an `https://` endpoint uses TLS. Trace exporter failures do not block allocation.
See the [tracing reference](https://docs.rbln.ai/latest/software/system_management/kubernetes/device_plugin.html#npu-allocation-tracing)
for span attributes.

## Binary options

These defaults describe this checkout. Flags override their corresponding environment
variables. Changing a path also requires matching host mounts in the deployment.

| Flag | Environment variable | Default / purpose |
| --- | --- | --- |
| `--cdi-root` | `CDI_ROOT` | `/var/run/cdi` |
| `--kubelet-device-plugin-path` | `KUBELET_DEVICE_PLUGIN_PATH` | `/var/lib/kubelet/device-plugins` |
| `--healthcheck-port` | `HEALTHCHECK_PORT` | `51515`; negative disables the service |
| `--device-scan-interval` | `DEVICE_SCAN_INTERVAL` | `5s`; refresh device inventory |
| `--otlp-endpoint` | `OTEL_EXPORTER_OTLP_ENDPOINT` | Empty; tracing disabled |

The binary has no log-level CLI flag. Use the logging environment variables above.
Resource names (`rebellions.ai/npu`, `rebellions.ai/npu-vf1`, `rebellions.ai/npu-vf4`)
are fixed. Runtime CDI support and the Container Toolkit's common runtime spec
are required; see [runtime configuration](https://docs.rbln.ai/latest/software/system_management/kubernetes/device_plugin.html).

Next: [diagnose allocation problems](../guides/troubleshoot-allocation.md).
