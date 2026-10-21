<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from charts/pacto-operator/values.yaml + Chart.yaml.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# Helm reference

- **Chart**: `pacto-operator`
- **Chart version**: `6.0.1`
- **App version**: `6.0.1`

Values are generated from `charts/pacto-operator/values.yaml`. Descriptions come from the chart's `# --` value annotations.

| Key | Default | Description |
| --- | --- | --- |
| `affinity` | `{}` | Affinity rules for the controller pod |
| `controller.enableMetricsObservation` | `false` | Observe the metrics capability (ServiceMonitor, PodMonitor, annotation or named-port discovery plus an active scrape). Also grants read access to ServiceMonitors and PodMonitors. Off by default; when off, metrics read unsupported. |
| `controller.enableProbing` | `false` | Actively probe in-cluster endpoints: the health capability over HTTP and the wire protocol of each present interface. Off by default; when off, health uses passive readiness-probe and EndpointSlice signals only and interface protocols read unverified. |
| `controller.kernelEvidence.enabled` | `false` | Merge the node agent's kernel observations (NodeRuntimeObservation objects) into interface and dependency evidence. Needs the node agent running; off by default, so evidence comes from the Kubernetes API only. Kernel evidence is on whenever this OR nodeAgent.enabled is true: a managed agent whose observations nobody merges would be pure cost. Set this alone only for an agent you run yourself. |
| `controller.kernelEvidence.namespace` | `""` | Namespace the node agent publishes to. Empty uses nodeAgent.namespace. |
| `controller.kernelEvidence.staleness` | `""` | How old a node's heartbeat may be before its evidence reads stale. Empty uses three times the node agent's publish interval. Accepts a Go duration (e.g. "2m"). |
| `controller.stabilizationWindow` | `""` | Stabilization window: how long a sustained NEGATIVE observation must persist before it converts to a confirmed violation (a transient blip reads Unknown meanwhile). Empty string uses the controller default (2m). Accepts a Go duration (e.g. "30s", "5m"). |
| `controller.watchNamespace` | `""` | Restrict the controller's observation scope to a single namespace. Empty string (default) means cluster-wide: the controller watches all namespaces. The dashboard inherits this scope automatically. |
| `dashboard.auth.agentGroups` | `[]` | Groups whose members are agents, as the delivery chart's `identities.agentGroups` (empty: `pacto:agents`) |
| `dashboard.auth.breakGlassGroups` | `[]` | Groups that may override, as the delivery chart's `identities.breakGlassGroups` (empty: `pacto:break-glass`) |
| `dashboard.auth.oidc.clientID` | `""` | The dashboard's client id at the issuer |
| `dashboard.auth.oidc.clientSecretRef.key` | `client-secret` | Key of the client secret in that Secret |
| `dashboard.auth.oidc.clientSecretRef.name` | `""` | Secret in the operator namespace holding the client secret (passed by secretKeyRef) |
| `dashboard.auth.oidc.extraScopes` | `[]` | Scopes asked for beyond openid, profile and groups (for example a cross-client audience scope) |
| `dashboard.auth.oidc.issuerURL` | `""` | OpenID Connect issuer URL people sign in with (empty: no sign-in) |
| `dashboard.auth.oidc.redirectURL` | `""` | The dashboard's `/auth/callback` URL as browsers reach it |
| `dashboard.auth.teamGroupPrefix` | `""` | A contract owner team `T` is the group `T` or this prefix followed by `T`, as the delivery chart is configured |
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
| `dashboard.miraURL` | `""` | Mira's address, to link the health-check citations in delivery views |
| `dashboard.observation.sources` | `[]` | Offline OTLP/JSON trace exports to mount read-only into the dashboard, so its Operational Graph can reconcile declared dependencies against observed ones. Each entry is one named data source: `name` is its stable Data Source identity (a DNS-1123 label) — reordering this list never renames a source, and it must not collide with any other data source the dashboard has (the live cluster, OCI, the disk cache, local bundles, an Evidence Server); `file` is the trace file's name directly inside that source's mount — a single path segment, so mount a claim whose export sits at the top of its own directory; and exactly one of `existingClaim` (an existing PVC, for real exports) or `configMap` (small static exports only, ~1 MiB cap) supplies it. Pacto reads exactly the declared files — through a root it cannot follow a symlink out of — never writes to them and never scans the mount. Whoever owns that storage owns producing and rotating the exports. This configures OFFLINE input only: Pacto ships no OTLP receiver and no trace collector (the optional node agent is an eBPF socket observer, not a trace pipeline). |
| `dashboard.ociSecrets` | `[]` | List of Secret names for OCI registry credentials. Supports Opaque (keys: registry + token, or registry + username + password) and kubernetes.io/dockerconfigjson secrets. When set, credentials from all secrets are merged; later secrets override earlier ones for the same registry. |
| `dashboard.resources.limits.memory` | `512Mi` | Memory limit for the dashboard container. The earlier default of 128Mi was OOMKilled while monitoring several OCI repositories and CRs at once; raise it further if the dashboard tracks a large fleet. |
| `dashboard.resources.requests.cpu` | `50m` | CPU request for the dashboard container |
| `dashboard.resources.requests.memory` | `128Mi` | Memory request for the dashboard container |
| `dashboard.service.nodePort` | `""` | Node port (only used when type is NodePort) |
| `dashboard.service.port` | `3000` | Dashboard service port |
| `dashboard.service.type` | `ClusterIP` | Dashboard exposure Service type (ClusterIP, NodePort, LoadBalancer). The operator manages an internal ClusterIP Service (pacto-dashboard) for pod-to-pod communication. This chart-managed Service provides configurable external access and serves as the backend for Ingress/HTTPRoute resources. Selects dashboard pods via operator-defined labels. |
| `evidence.enabled` | `false` | Enable the operator-managed Evidence Server deployment. Disabled by default. When enabled the operator reconciles a SEPARATE Evidence Server Deployment and an internal Service; the image is the same runtime image as the dashboard and is not user-configurable. The Evidence Server is single- writer, so its replica count is fixed at one. |
| `evidence.httpRoute.enabled` | `false` | Enable Gateway API HTTPRoute for the Evidence Server. |
| `evidence.httpRoute.hostnames` | `[]` | Hostnames for the HTTPRoute. |
| `evidence.httpRoute.parentRefs` | `[]` | Parent gateway references. |
| `evidence.httpRoute.rules` | `[]` | HTTPRoute rules. When empty, a single catch-all rule routes to the chart-managed evidence Service on evidence.service.port. |
| `evidence.ingress.annotations` | `{}` | Ingress annotations. |
| `evidence.ingress.className` | `""` | Ingress class name. |
| `evidence.ingress.enabled` | `false` | Enable Ingress for the Evidence Server. |
| `evidence.ingress.hosts` | `null` | Ingress hosts. |
| `evidence.ingress.tls` | `[]` | Ingress TLS configuration. |
| `evidence.registry.credentialsSecret` | `""` | Optional name of an EXISTING kubernetes.io/dockerconfigjson Secret with credentials for that registry, mounted read-only. The chart never creates it and never renders its contents. Empty means anonymous or in-cluster access. |
| `evidence.registry.subjects` | `[]` | Required when evidence is enabled: the exact, immutable contract revisions evidence may be reported against, each an oci://&lt;repo&gt;@sha256:&lt;digest&gt; reference. The registry holding them IS the durable evidence store — every accepted record is published as an OCI 1.1 referrer of one of these manifests — so the chart installs nothing durable in the cluster. A mutable tag is rejected: it could be moved onto another manifest and silently change what the stored evidence reports on. That registry must serve the native OCI 1.1 Referrers API — GHCR and CNCF distribution (registry:2, registry:3) do not, and the Evidence Server stays permanently not-ready against them. |
| `evidence.resources.limits.memory` | `256Mi` | Memory limit for the Evidence Server container. It verifies and forwards one envelope at a time and stores nothing locally, so this is sized for a single writer. |
| `evidence.resources.requests.cpu` | `25m` | CPU request for the Evidence Server container |
| `evidence.resources.requests.memory` | `64Mi` | Memory request for the Evidence Server container |
| `evidence.service.nodePort` | `""` | Node port (only used when type is NodePort). |
| `evidence.service.port` | `8686` | Evidence service port. |
| `evidence.service.type` | `ClusterIP` | Evidence exposure Service type (ClusterIP, NodePort, LoadBalancer). The operator manages an internal ClusterIP Service (pacto-evidence) for in-cluster access; this chart-managed Service provides optional external access and backs any Ingress/HTTPRoute. |
| `evidence.trust.existingSecret` | `""` | Name of an existing Secret of trusted producer public keys, mounted read-only. Signature verification is mandatory, so this is required when evidence is enabled. |
| `fullnameOverride` | `""` | Override the full release name |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `image.repository` | `ghcr.io/trianalab/pacto/operator` | Controller image repository |
| `image.tag` | `""` | Overrides the image tag (default is the chart appVersion) |
| `imagePullSecrets` | `[]` | Image pull secrets for private registries |
| `insecureRegistries` | `[]` | Registry hosts (`host:port`) to reach over plain HTTP instead of HTTPS, for a controlled in-cluster registry. Scoped per host, so every other registry stays HTTPS-only. The controller, the managed dashboard and the managed Evidence Server all inherit it — each resolves contract refs itself. |
| `leaderElection.enabled` | `true` | Enable leader election for HA deployments |
| `metrics.enabled` | `true` | Enable the metrics endpoint |
| `metrics.secure` | `true` | Serve metrics over HTTPS |
| `metrics.service.port` | `8443` | Metrics service port |
| `metrics.serviceMonitor.enabled` | `false` | Create a Prometheus ServiceMonitor |
| `metrics.serviceMonitor.interval` | `""` | Scrape interval |
| `metrics.serviceMonitor.scrapeTimeout` | `""` | Scrape timeout |
| `nameOverride` | `""` | Override the chart name |
| `nodeAgent.enabled` | `false` | Deploy the node agent DaemonSet. Off by default. |
| `nodeAgent.namespace` | `pacto-node-agent` | Dedicated namespace the agents run and publish in. The chart creates it and labels it privileged for Pod Security; it must not be the release namespace and must not exist outside this release. |
| `nodeAgent.publishInterval` | `30s` | How often each agent publishes its node's observation. A Go duration; kernel evidence reads stale after three intervals unless controller.kernelEvidence.staleness says otherwise. |
| `nodeAgent.resources.limits.cpu` | `200m` | CPU limit for the node agent container |
| `nodeAgent.resources.limits.memory` | `256Mi` | Memory limit for the node agent container. The agent caps the pods and connections it tracks, and the kernel ring buffer is charged here. |
| `nodeAgent.resources.requests.cpu` | `50m` | CPU request for the node agent container |
| `nodeAgent.resources.requests.memory` | `64Mi` | Memory request for the node agent container |
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
