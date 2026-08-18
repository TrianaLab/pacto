<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from `go run ./integrations/kubernetes/cmd --help` (real output) + cmd/main.go.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# Operator configuration

Controller flags and their exact defaults are captured from the operator's real `--help` output. Set them via chart values or by editing the Deployment args.

## Command-line flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `-dashboard-cpu-limit` | `string` |  | CPU limit for the dashboard container (e.g. 200m). Empty uses the built-in default. |
| `-dashboard-cpu-request` | `string` |  | CPU request for the dashboard container (e.g. 50m). Empty uses the built-in default. |
| `-dashboard-memory-limit` | `string` |  | Memory limit for the dashboard container (e.g. 512Mi). Empty uses the built-in default. |
| `-dashboard-memory-request` | `string` |  | Memory request for the dashboard container (e.g. 128Mi). Empty uses the built-in default. |
| `-dashboard-oci-secret` | `string` |  | Optional: name of a Secret in the operator namespace containing OCI registry credentials. Supports Opaque (registry + token, or registry + username + password) and kubernetes.io/dockerconfigjson secrets. Ignored when --dashboard-oci-secrets is set. |
| `-dashboard-oci-secrets` | `string` |  | Optional: comma-separated list of Secret names in the operator namespace for OCI registry credentials. Takes precedence over --dashboard-oci-secret. |
| `-dashboard-trace-source` | `value` |  | Repeatable: an offline OTLP/JSON trace file to mount read-only into the dashboard, as name=NAME,file=RELATIVE_PATH,existingClaim=PVC (or configMap=NAME). NAME is the stable Data Source identity. Configures offline input only; Pacto runs no OTLP receiver. |
| `-enable-dashboard` | `bool` |  | Enable the managed Pacto dashboard deployment. Disabled by default. |
| `-enable-evidence-server` | `bool` |  | Enable the managed Pacto Evidence Server deployment. Disabled by default. |
| `-enable-http2` | `bool` |  | If set, HTTP/2 will be enabled for the metrics and webhook servers |
| `-enable-metrics-observation` | `bool` |  | Enable full metrics observation (discovery + active probe). When disabled, metrics dimension returns Unsupported. |
| `-enable-probing` | `bool` |  | Enable active in-cluster HTTP probing of health capability endpoints (Tier A). Off by default; when off, health uses passive readiness-probe and EndpointSlice signals only. |
| `-evidence-cpu-limit` | `string` |  | CPU limit for the Evidence Server container. Empty uses the built-in default. |
| `-evidence-cpu-request` | `string` |  | CPU request for the Evidence Server container. Empty uses the built-in default. |
| `-evidence-credentials-secret` | `string` |  | Optional: name of an existing kubernetes.io/dockerconfigjson Secret with contract-registry credentials, mounted read-only. Empty means anonymous or in-cluster registry access. |
| `-evidence-memory-limit` | `string` |  | Memory limit for the Evidence Server container. Empty uses the built-in default. |
| `-evidence-memory-request` | `string` |  | Memory request for the Evidence Server container. Empty uses the built-in default. |
| `-evidence-subject` | `value` |  | Repeatable: an exact contract revision evidence is stored on, as oci://<repo>@sha256:<digest>. The registry holding it IS the durable evidence store — accepted records are published as OCI 1.1 referrers of that manifest. At least one is required when the Evidence Server is enabled. |
| `-evidence-trust-secret` | `string` |  | Name of a Secret of trusted producer public keys, mounted read-only. Required when the Evidence Server is enabled. |
| `-health-probe-bind-address` | `string` | `:8081` | The address the probe endpoint binds to. |
| `-interface-name-match-discovery` | `bool` |  | Enable resolving an unbound interface's Service port by matching a Service port whose name equals the interface name (positive availability assist only; never produces an absent or error result). |
| `-kubeconfig` | `string` |  | Paths to a kubeconfig. Only required if out-of-cluster. |
| `-leader-elect` | `bool` |  | Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager. |
| `-metrics-bind-address` | `string` | `:8080` | The address the metrics endpoint binds to. Use :8443 for HTTPS or :8080 for HTTP, or set to 0 to disable the metrics service. |
| `-metrics-cert-key` | `string` | `tls.key` | The name of the metrics server key file. |
| `-metrics-cert-name` | `string` | `tls.crt` | The name of the metrics server certificate file. |
| `-metrics-cert-path` | `string` |  | The directory that contains the metrics server certificate. |
| `-metrics-secure` | `bool` |  | If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=true to enable HTTPS. |
| `-stabilization-window` | `duration` | `2m0s` | The stabilization window duration for compliance assertions before they trigger a false condition. Assertions must remain unsatisfied for this entire window before being considered a failure. |
| `-version` | `bool` |  | Print version information and exit. |
| `-watch-namespace` | `string` |  | Restrict the controller to watch a single namespace. Empty (default) means cluster-wide. The dashboard inherits this scope automatically. |
| `-webhook-cert-key` | `string` | `tls.key` | The name of the webhook key file. |
| `-webhook-cert-name` | `string` | `tls.crt` | The name of the webhook certificate file. |
| `-webhook-cert-path` | `string` |  | The directory that contains the webhook certificate. |
| `-zap-devel` | `bool` | `true` | Development Mode defaults(encoder=consoleEncoder,logLevel=Debug,stackTraceLevel=Warn). Production Mode defaults(encoder=jsonEncoder,logLevel=Info,stackTraceLevel=Error) |
| `-zap-encoder` | `value` |  | Zap log encoding (one of 'json' or 'console') |
| `-zap-log-level` | `value` |  | Zap Level to configure the verbosity of logging. Can be one of 'debug', 'info', 'error', 'panic' or any integer value > 0 which corresponds to custom debug levels of increasing verbosity |
| `-zap-stacktrace-level` | `value` |  | Zap Level at and above which stacktraces are captured (one of 'info', 'error', 'panic'). |
| `-zap-time-encoding` | `value` |  | Zap time encoding (one of 'epoch', 'millis', 'nano', 'iso8601', 'rfc3339' or 'rfc3339nano'). Defaults to 'epoch'. |

## Environment variables

Read directly by the controller entrypoint (`cmd/main.go`), typically wired through the downward API in the chart's Deployment.

| Variable | Purpose |
| --- | --- |
| `OPERATOR_DEPLOYMENT_NAME` | Operator Deployment name, used to set ownerReferences on dashboard resources. |
| `POD_NAMESPACE` | Namespace the operator (and its managed dashboard) runs in. Required. |
