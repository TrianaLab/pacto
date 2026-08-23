<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from `go run ./integrations/kubernetes/cmd --help` (real output) + cmd/main.go.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# Operator configuration

Controller flags and their exact defaults are captured from the operator's real `--help` output.

**One dash or two makes no difference.** The table shows the single-dash spelling because that is how Go's `flag` package prints and reports flags, and other pages write `--enable-metrics-observation`. The parser accepts both forms identically: `--enable-metrics-observation=x` and `-enable-metrics-observation=x` produce the same `invalid boolean value "x" for -enable-metrics-observation` error. Neither spelling is more correct.

**Two different defaults can apply to the same flag.** The Default column below is the *binary's* default -- what you get running the controller with no arguments. The Helm chart renders its own fixed argument list, so where a chart value exists it decides, and its default may differ. `-enable-dashboard` is the one that catches people out: the binary defaults it off, the chart's `dashboard.enabled` defaults it on, and a chart install therefore runs the managed dashboard. See the [Helm reference](helm-reference.md) for the values and their defaults.

**A flag the chart never renders cannot be set on the documented install path.** The chart has no `extraArgs`, so the *Via chart* column is the whole story: `chart value` means some value in `values.yaml` renders this flag; `always on` means the chart hardcodes it and no value changes it; `no` means the chart never passes it, and reaching it requires patching the Deployment after install -- which `helm upgrade` then reverts. See [Limitations](limitations.md#opt-in-features).

## Command-line flags

| Flag | Type | Default | Via chart | Description |
| --- | --- | --- | --- | --- |
| `-dashboard-cpu-limit` | `string` |  | chart value | CPU limit for the dashboard container (e.g. 200m). Empty uses the built-in default. |
| `-dashboard-cpu-request` | `string` |  | chart value | CPU request for the dashboard container (e.g. 50m). Empty uses the built-in default. |
| `-dashboard-memory-limit` | `string` |  | chart value | Memory limit for the dashboard container (e.g. 512Mi). Empty uses the built-in default. |
| `-dashboard-memory-request` | `string` |  | chart value | Memory request for the dashboard container (e.g. 128Mi). Empty uses the built-in default. |
| `-dashboard-oci-secret` | `string` |  | chart value | Optional: name of a Secret in the operator namespace containing OCI registry credentials. Supports Opaque (registry + token, or registry + username + password) and kubernetes.io/dockerconfigjson secrets. Ignored when --dashboard-oci-secrets is set. |
| `-dashboard-oci-secrets` | `string` |  | chart value | Optional: comma-separated list of Secret names in the operator namespace for OCI registry credentials. Takes precedence over --dashboard-oci-secret. |
| `-dashboard-trace-source` | `value` |  | chart value | Repeatable: an offline OTLP/JSON trace file to mount read-only into the dashboard, as name=NAME,file=RELATIVE_PATH,existingClaim=PVC (or configMap=NAME). NAME is the stable Data Source identity. Configures offline input only; Pacto runs no OTLP receiver. |
| `-enable-dashboard` | `bool` |  | chart value | Enable the managed Pacto dashboard deployment. Disabled by default. |
| `-enable-evidence-server` | `bool` |  | chart value | Enable the managed Pacto Evidence Server deployment. Disabled by default. |
| `-enable-http2` | `bool` |  | no | If set, HTTP/2 will be enabled for the metrics and webhook servers |
| `-enable-metrics-observation` | `bool` |  | no | Enable full metrics observation (discovery + active probe). When disabled, metrics dimension returns Unsupported. |
| `-enable-probing` | `bool` |  | no | Enable active in-cluster HTTP probing of health capability endpoints (Tier A). Off by default; when off, health uses passive readiness-probe and EndpointSlice signals only. |
| `-evidence-cpu-limit` | `string` |  | chart value | CPU limit for the Evidence Server container. Empty uses the built-in default. |
| `-evidence-cpu-request` | `string` |  | chart value | CPU request for the Evidence Server container. Empty uses the built-in default. |
| `-evidence-credentials-secret` | `string` |  | chart value | Optional: name of an existing kubernetes.io/dockerconfigjson Secret with contract-registry credentials, mounted read-only. Empty means anonymous or in-cluster registry access. |
| `-evidence-memory-limit` | `string` |  | chart value | Memory limit for the Evidence Server container. Empty uses the built-in default. |
| `-evidence-memory-request` | `string` |  | chart value | Memory request for the Evidence Server container. Empty uses the built-in default. |
| `-evidence-subject` | `value` |  | chart value | Repeatable: an exact contract revision evidence is stored on, as oci://&lt;repo&gt;@sha256:&lt;digest&gt;. The registry holding it IS the durable evidence store — accepted records are published as OCI 1.1 referrers of that manifest. At least one is required when the Evidence Server is enabled. |
| `-evidence-trust-secret` | `string` |  | chart value | Name of a Secret of trusted producer public keys, mounted read-only. Required when the Evidence Server is enabled. |
| `-health-probe-bind-address` | `string` | `:8081` | always on | The address the probe endpoint binds to. |
| `-interface-name-match-discovery` | `bool` |  | no | Enable resolving an unbound interface's Service port by matching a Service port whose name equals the interface name (positive availability assist only; never produces an absent or error result). |
| `-kubeconfig` | `string` |  | no | Paths to a kubeconfig. Only required if out-of-cluster. |
| `-leader-elect` | `bool` |  | chart value | Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager. |
| `-metrics-bind-address` | `string` | `:8080` | chart value | The address the metrics endpoint binds to. Use :8443 for HTTPS or :8080 for HTTP, or set to 0 to disable the metrics service. |
| `-metrics-cert-key` | `string` | `tls.key` | no | The name of the metrics server key file. |
| `-metrics-cert-name` | `string` | `tls.crt` | no | The name of the metrics server certificate file. |
| `-metrics-cert-path` | `string` |  | no | The directory that contains the metrics server certificate. |
| `-metrics-secure` | `bool` |  | chart value | If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=true to enable HTTPS. |
| `-stabilization-window` | `duration` | `2m0s` | chart value | The stabilization window duration for compliance assertions before they trigger a false condition. Assertions must remain unsatisfied for this entire window before being considered a failure. |
| `-version` | `bool` |  | no | Print version information and exit. |
| `-watch-namespace` | `string` |  | chart value | Restrict the controller to watch a single namespace. Empty (default) means cluster-wide. The dashboard inherits this scope automatically. |
| `-webhook-cert-key` | `string` | `tls.key` | no | The name of the webhook key file. |
| `-webhook-cert-name` | `string` | `tls.crt` | no | The name of the webhook certificate file. |
| `-webhook-cert-path` | `string` |  | no | The directory that contains the webhook certificate. |
| `-zap-devel` | `bool` | `true` | no | Development Mode defaults(encoder=consoleEncoder,logLevel=Debug,stackTraceLevel=Warn). Production Mode defaults(encoder=jsonEncoder,logLevel=Info,stackTraceLevel=Error) |
| `-zap-encoder` | `value` |  | no | Zap log encoding (one of 'json' or 'console') |
| `-zap-log-level` | `value` |  | no | Zap Level to configure the verbosity of logging. Can be one of 'debug', 'info', 'error', 'panic' or any integer value > 0 which corresponds to custom debug levels of increasing verbosity |
| `-zap-stacktrace-level` | `value` |  | no | Zap Level at and above which stacktraces are captured (one of 'info', 'error', 'panic'). |
| `-zap-time-encoding` | `value` |  | no | Zap time encoding (one of 'epoch', 'millis', 'nano', 'iso8601', 'rfc3339' or 'rfc3339nano'). Defaults to 'epoch'. |

## Environment variables

Read directly by the controller entrypoint (`cmd/main.go`), typically wired through the downward API in the chart's Deployment.

| Variable | Purpose |
| --- | --- |
| `OPERATOR_DEPLOYMENT_NAME` | Operator Deployment name, used to set ownerReferences on dashboard resources. |
| `POD_NAMESPACE` | Namespace the operator (and its managed dashboard) runs in. Required. |
