<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from charts/pacto-operator/values.yaml + Chart.yaml.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# Helm reference

- **Chart**: `pacto-operator`
- **Chart version**: `5.1.1`
- **App version**: `5.1.1`

Values are generated from `charts/pacto-operator/values.yaml`. Descriptions come from the chart's `# --` value annotations.

| Key | Default | Description |
| --- | --- | --- |
| `affinity` | `{}` | Affinity rules for the controller pod |
| `controller.stabilizationWindow` | `""` | Stabilization window: how long a sustained NEGATIVE observation must persist before it converts to a confirmed violation (a transient blip reads Unknown meanwhile). Empty string uses the controller default (2m). Accepts a Go duration (e.g. "30s", "5m"). |
| `controller.watchNamespace` | `""` | Restrict the controller's observation scope to a single namespace. Empty string (default) means cluster-wide: the controller watches all namespaces. The dashboard inherits this scope automatically. |
| `dashboard.enabled` | `true` | Enable the operator-managed dashboard deployment. The dashboard image is controlled by the operator and derived from the bundled Pacto library version. It is not user-configurable. |
| `dashboard.httpRoute.enabled` | `false` | Enable Gateway API HTTPRoute for the dashboard |
| `dashboard.httpRoute.hostnames` | `[]` | Hostnames for the HTTPRoute |
| `dashboard.httpRoute.parentRefs` | `[]` | Parent gateway references |
| `dashboard.httpRoute.rules` | `[]` | HTTPRoute rules. When empty, a single catch-all rule is generated that routes all traffic to the chart-managed dashboard Service on dashboard.service.port. |
| `dashboard.ingress.annotations` | `{}` | Ingress annotations |
| `dashboard.ingress.className` | `""` | Ingress class name |
| `dashboard.ingress.enabled` | `false` | Enable Ingress for the dashboard |
| `dashboard.ingress.hosts` | `null` | Ingress hosts |
| `dashboard.ingress.tls` | `[]` | Ingress TLS configuration |
| `dashboard.ociSecret` | `""` | Optional Secret name for OCI registry credentials (backward compatible). Supports Opaque secrets (keys: registry + token, or registry + username + password) and kubernetes.io/dockerconfigjson secrets. Ignored when ociSecrets is set. |
| `dashboard.ociSecrets` | `[]` | List of Secret names for OCI registry credentials. Supports Opaque (with registry key) and kubernetes.io/dockerconfigjson secrets. When set, credentials from all secrets are merged; later secrets override earlier ones for the same registry. Takes precedence over ociSecret. |
| `dashboard.resources.limits.memory` | `512Mi` |  |
| `dashboard.resources.requests.cpu` | `50m` |  |
| `dashboard.resources.requests.memory` | `128Mi` |  |
| `dashboard.service.nodePort` | `""` | Node port (only used when type is NodePort) |
| `dashboard.service.port` | `3000` | Dashboard service port |
| `dashboard.service.type` | `ClusterIP` | Dashboard exposure Service type (ClusterIP, NodePort, LoadBalancer). The operator manages an internal ClusterIP Service (pacto-dashboard) for pod-to-pod communication. This chart-managed Service provides configurable external access and serves as the backend for Ingress/HTTPRoute resources. Selects dashboard pods via operator-defined labels. |
| `fullnameOverride` | `""` | Override the full release name |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `image.repository` | `ghcr.io/trianalab/pacto/operator` | Controller image repository |
| `image.tag` | `""` | Overrides the image tag (default is the chart appVersion) |
| `imagePullSecrets` | `[]` | Image pull secrets for private registries |
| `leaderElection.enabled` | `true` | Enable leader election for HA deployments |
| `metrics.enabled` | `true` | Enable the metrics endpoint |
| `metrics.secure` | `true` | Serve metrics over HTTPS |
| `metrics.service.port` | `8443` | Metrics service port |
| `metrics.serviceMonitor.enabled` | `false` | Create a Prometheus ServiceMonitor |
| `metrics.serviceMonitor.interval` | `""` | Scrape interval |
| `metrics.serviceMonitor.scrapeTimeout` | `""` | Scrape timeout |
| `nameOverride` | `""` | Override the chart name |
| `nodeSelector` | `{}` | Node selector for the controller pod |
| `podAnnotations` | `{}` | Annotations to add to the controller pod |
| `podLabels` | `{}` | Labels to add to the controller pod |
| `podSecurityContext.runAsNonRoot` | `true` | Run pod as non-root |
| `podSecurityContext.seccompProfile.type` | `RuntimeDefault` | Seccomp profile type |
| `replicaCount` | `1` | Number of controller replicas |
| `resources.limits.cpu` | `500m` | CPU limit |
| `resources.limits.memory` | `128Mi` | Memory limit |
| `resources.requests.cpu` | `10m` | CPU request |
| `resources.requests.memory` | `64Mi` | Memory request |
| `securityContext.allowPrivilegeEscalation` | `false` | Disallow privilege escalation |
| `securityContext.capabilities.drop` | `null` | Drop all capabilities |
| `securityContext.readOnlyRootFilesystem` | `true` | Read-only root filesystem |
| `securityContext.runAsNonRoot` | `true` | Run as non-root |
| `securityContext.runAsUser` | `65532` | Run as UID 65532 (nonroot in distroless) |
| `serviceAccount.annotations` | `{}` | Annotations to add to the ServiceAccount |
| `serviceAccount.automount` | `true` | Automount API credentials |
| `serviceAccount.create` | `true` | Create a ServiceAccount for the controller |
| `serviceAccount.name` | `""` | Name override (defaults to fullname) |
| `tolerations` | `[]` | Tolerations for the controller pod |
