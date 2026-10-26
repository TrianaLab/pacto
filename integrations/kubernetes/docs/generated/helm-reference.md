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
| `dashboard.auth.deliveryNamespace` | `""` | Namespace where Pacto delivery runs. The dashboard reads the identity policy it publishes there (agent, break-glass and human groups, team prefix), so it is never configured twice (empty: `delivery.namespace` when `delivery.enabled`, else `pacto-delivery`) |
| `dashboard.auth.oidc.clientID` | `""` | The dashboard's client id at the issuer |
| `dashboard.auth.oidc.clientSecretRef.key` | `client-secret` | Key of the client secret in that Secret |
| `dashboard.auth.oidc.clientSecretRef.name` | `""` | Secret in the operator namespace holding the client secret (passed by secretKeyRef) |
| `dashboard.auth.oidc.extraScopes` | `[]` | Scopes asked for beyond openid, profile and groups (for example a cross-client audience scope) |
| `dashboard.auth.oidc.issuerURL` | `""` | OpenID Connect issuer URL people sign in with (empty: no sign-in) |
| `dashboard.auth.oidc.redirectURL` | `""` | The dashboard's `/auth/callback` URL as browsers reach it |
| `dashboard.auth.required` | `false` | Gate the fleet views and the whole API behind sign-in too, not only delivery (needs `oidc.issuerURL`) |
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
| `delivery.admissionPolicies.allowUnguarded` | `false` | Render without the policies (enabled=false) anyway, and skip the chart's Kubernetes 1.30 and ValidatingAdmissionPolicy API check. Without the policies anyone who may create an Operation or an Approval can claim to be anyone, so the chart refuses either unless this is set. |
| `delivery.admissionPolicies.enabled` | `true` | Install the ValidatingAdmissionPolicies (Kubernetes 1.30 or later): an operation's actor is the requesting user, only the promoter may set witnessed, override and unfreeze are limited to their raisers, cancel is refused once the pointer moved and an agent cancels only its own operations, approvals come from people and never change, spec.ref.digest is edited only by the promoter or break-glass, and on objects labelled delivery.pacto.trianalab.io/apply-ref the label, the source and what a Kustomization applies change only by break-glass, and only the promoter writes the status of a Stage or an Operation. The promoter admits no operation and counts no approval while any of them is missing or does not deny. |
| `delivery.admissionPolicies.gitopsUsers` | `null` | Users besides break-glass that may create or delete an OCIRepository, Kustomization or Application labelled delivery.pacto.trianalab.io/apply-ref: the GitOps controllers that apply them. Deleting one and creating it again is an edit, so a namespace editor may do neither. The namespace controller and the garbage collector are always allowed. |
| `delivery.affinity` | `{}` | Affinity for the promoter pod |
| `delivery.argocd.enabled` | `false` | Let Stages name an Argo CD Application as their applier (`spec.applyRef.kind: Application`). Grants the promoter patch on `argoproj.io` Applications (it only sets `spec.source.targetRevision`, and the pointer admission policy holds it to that) and read access to the agent, approver and recorder roles. Off by default. |
| `delivery.createNamespace` | `true` | Create `delivery.namespace`. Set false to create it yourself first, for example to put the recorder key Secret in it before the install |
| `delivery.enabled` | `false` | Install Pacto delivery: the promoter, its CRDs (Stage, Operation, Approval), roles and admission policies |
| `delivery.gates.providers` | `[]` | Health gate providers a Stage may name in `spec.gates.verify.checks[].provider`, each `{name, type, url}` with type `mira` (Mira's query API), `http` (any tool behind the plug-in format) or `eval` (a runner for the check suites a version ships, named by `checks[].suite`). The URL is operator configuration: a Stage only names a provider and an agent names nothing. `pacto.contract` and `pacto.runtime` are built in. The URL must be https: a verdict ends up in a signed record, so an answer forged in transit could get a Pass signed. Plain http works only on loopback, or with `allowPlainHTTP: true` on a test cluster. An `eval` provider also needs `signers`: the runners whose signed answers it trusts, each `{name, publicKey}` or `{name, identity, issuer, trustedRoot}`. An answer no signer signed is Unknown, and the verdict names the runner that signed it. |
| `delivery.identities.agentGroups` | `null` | Groups whose members are agents. Agents may propose, never approve or unfreeze. Service accounts other than the promoter are always agents. |
| `delivery.identities.breakGlassGroups` | `null` | Groups that may raise an override and edit an OCIRepository's spec.ref.digest directly |
| `delivery.identities.humanGroups` | `[]` | When set, only members of these groups count as people. Anyone in none of them is an agent, so an agent identity nobody enrolled in an agent group still cannot approve. Empty classifies by `agentGroups` alone. |
| `delivery.identities.requireOwnerTeam` | `true` | A held question whose service and consumers declare no owner team is answered by nobody, and only break-glass can override. Set false to let any person answer it, as before. |
| `delivery.identities.teamGroupPrefix` | `""` | Only members of the teams a held question names may answer it. A contract owner team `T` is the identity-provider group `T`, or once this prefix is set only this prefix followed by `T` (for example `acme:`). A `system:` group is never a team. |
| `delivery.image.digest` | `""` | Pins the image by digest (sha256:...), appended to the tag as tag@digest so the runtime pulls exactly that image whatever the tag resolves to later |
| `delivery.image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `delivery.image.repository` | `ghcr.io/trianalab/pacto/delivery` | Promoter image repository (the pacto-delivery image) |
| `delivery.image.tag` | `""` | Overrides the image tag (default is the chart appVersion) |
| `delivery.imagePullSecrets` | `[]` | Image pull secrets for private registries |
| `delivery.insecureRegistries` | `[]` | Registry hosts (`host:port`) the promoter and the recorder reach over plain HTTP. An OCIRepository with `spec.insecure: true` is read over HTTP anyway. |
| `delivery.metrics.enabled` | `true` | Expose the promoter's metrics (plain HTTP, port 8080) through a Service. The recorder's come with `delivery.recorder.monitoring` |
| `delivery.metrics.serviceMonitor.enabled` | `false` | Create a Prometheus Operator ServiceMonitor for that Service |
| `delivery.metrics.serviceMonitor.interval` | `""` | Scrape interval (empty uses Prometheus' default) |
| `delivery.metrics.serviceMonitor.scrapeTimeout` | `""` | Scrape timeout (empty uses Prometheus' default) |
| `delivery.namespace` | `pacto-delivery` | Dedicated namespace the promoter and the recorder run in. The chart creates it; it must not be the release or node agent namespace (the operator can create Deployments, DaemonSets and Secrets there). |
| `delivery.nodeSelector` | `{}` | Node selector for the promoter pod |
| `delivery.podSecurityContext.runAsNonRoot` | `true` | Run pod as non-root |
| `delivery.podSecurityContext.seccompProfile.type` | `RuntimeDefault` | Seccomp profile type |
| `delivery.promoter.authFreshness` | `15m` | How old a person's login may be when their approval is given; an older one is ignored (a Go duration). Every Stage publishes it in `status.authFreshness` |
| `delivery.promoter.leaderElect` | `true` | Elect one active promoter |
| `delivery.promoter.poll` | `3s` | How often an operation waiting on Flux, Pacto or a person is looked at (a Go duration) |
| `delivery.promoter.registryCredentials.secretName` | `""` | A `kubernetes.io/dockerconfigjson` Secret with read access to the contract and deploy repositories, for a private registry. Without it the promoter reads only public ones. |
| `delivery.promoter.upstreamTrust` | `""` | The recorder keys of other clusters, as a trust config in YAML (keys per stream glob). A Stage with `candidates.upstreamRecords` promotes only on records one of these keys signed for that stream; without it such a Stage is not Ready. Public keys only. |
| `delivery.promoter.workers` | `4` | How many operations and Stages the promoter reconciles at once, so one slow health provider does not hold up the rest |
| `delivery.recorder.cdeventsSink` | `""` | A URL the recorder POSTs a CDEvent (CloudEvents, structured mode) to for every record, best effort. Empty sends none. |
| `delivery.recorder.enabled` | `false` | Run the recorder, which writes a signed record of every concluded operation to the registry as OCI referrers. Off by default. When on, the promoter leaves the record finalizer to it. The registry must support the OCI Referrers API natively (GHCR and released `distribution` do not). |
| `delivery.recorder.extraEnv` | `[]` | Extra environment for the recorder (for example the credentials a KMS client reads) |
| `delivery.recorder.leaderElect` | `true` | Elect one active recorder |
| `delivery.recorder.monitoring.enabled` | `false` | Scrape the recorder's metrics and alert when records fall behind: a metrics Service, a ServiceMonitor and a PrometheusRule. Needs the Prometheus Operator CRDs. |
| `delivery.recorder.operationTTL` | `168h` | Keep a recorded operation this long after it concluded, then delete it: its record holds what happened. The operation a Stage runs is never deleted. `0s` keeps them all (a Go duration). |
| `delivery.recorder.poll` | `30s` | How long a registry or signer failure backs off (a Go duration) |
| `delivery.recorder.registryCredentials.secretName` | `""` | A `kubernetes.io/dockerconfigjson` Secret with push access to the streams, apply and contract repositories. Only the recorder mounts it. |
| `delivery.recorder.resources.limits.cpu` | `500m` | CPU limit |
| `delivery.recorder.resources.limits.memory` | `256Mi` | Memory limit |
| `delivery.recorder.resources.requests.cpu` | `10m` | CPU request |
| `delivery.recorder.resources.requests.memory` | `64Mi` | Memory request |
| `delivery.recorder.serviceAccount.annotations` | `{}` | Annotations to add to the recorder's ServiceAccount (workload identity for KMS or keyless) |
| `delivery.recorder.serviceAccount.create` | `true` | Create a ServiceAccount for the recorder |
| `delivery.recorder.serviceAccount.name` | `""` | Name override (defaults to `<fullname>-delivery-recorder`) |
| `delivery.recorder.signing.allowOperatorReadableKey` | `false` | Say yes to an Ed25519 key in a Secret. The operator reads every Secret in the cluster, so it can read this key too and the recorder is not its only holder. The chart refuses `ed25519.secretName` unless this is true. Prefer KMS or keyless signing |
| `delivery.recorder.signing.ed25519.key` | `key.pem` | The key in that Secret |
| `delivery.recorder.signing.ed25519.secretName` | `""` | A Secret holding a PKCS#8 PEM Ed25519 private key (for kind and tests) |
| `delivery.recorder.signing.keyless.enabled` | `false` | Sign keyless with a Fulcio certificate for the recorder's workload identity, presented as a projected service account token for the `sigstore` audience. Rekor upload is off; a timestamp authority dates each signature. |
| `delivery.recorder.signing.keyless.fulcioURL` | `""` | The Fulcio that issues the certificate |
| `delivery.recorder.signing.keyless.identity` | `""` | The certificate identity keyless signing produces |
| `delivery.recorder.signing.keyless.issuer` | `""` | The OIDC issuer keyless signing uses |
| `delivery.recorder.signing.keyless.timestampURL` | `""` | The timestamp authority whose signed time a short-lived certificate verifies at |
| `delivery.recorder.signing.keyless.trustedRoot` | `""` | The Sigstore trusted root (`trusted_root.json`) naming that Fulcio and timestamp authority. Required with keyless: without it a self-signed certificate could name the recorder's identity. |
| `delivery.recorder.signing.kms` | `""` | A KMS key URI (`awskms://`, `gcpkms://`, `azurekms://`, `hashivault://`). The default image includes every provider; an image built with `--build-arg GO_TAGS=` has none, and one built with `GO_TAGS=kms_aws` (or `kms_gcp`, `kms_azure`, `kms_hashivault`) has only that one |
| `delivery.recorder.streamsRepository` | `""` | Repository (`host/path`) that holds one anchor per Stage stream. Required when enabled. |
| `delivery.recorder.trust` | `""` | Other keys whose records the recorder accepts, as a trust config in YAML (`keys: [{publicKey: <PEM>, streams: ["*/prod/*"]}]`): list the old key here while you rotate to a new one, and set `untilSeq` and `untilRecord` on a leaked key to revoke it after the last record it signed. The recorder's own key is always trusted. A record no listed key signed is skipped as untrusted. |
| `delivery.replicaCount` | `1` | Number of promoter replicas. Only the elected leader acts. |
| `delivery.resources.limits.cpu` | `500m` | CPU limit |
| `delivery.resources.limits.memory` | `256Mi` | Memory limit |
| `delivery.resources.requests.cpu` | `10m` | CPU request |
| `delivery.resources.requests.memory` | `64Mi` | Memory request |
| `delivery.securityContext.allowPrivilegeEscalation` | `false` | Disallow privilege escalation |
| `delivery.securityContext.capabilities.drop` | `null` | Drop all capabilities |
| `delivery.securityContext.readOnlyRootFilesystem` | `true` | Read-only root filesystem |
| `delivery.securityContext.runAsNonRoot` | `true` | Run as non-root |
| `delivery.securityContext.runAsUser` | `65532` | Run as UID 65532 (nonroot in distroless) |
| `delivery.serviceAccount.annotations` | `{}` | Annotations to add to the ServiceAccount |
| `delivery.serviceAccount.create` | `true` | Create a ServiceAccount for the promoter |
| `delivery.serviceAccount.name` | `""` | Name override (defaults to `<fullname>-delivery`) |
| `delivery.tolerations` | `[]` | Tolerations for the promoter pod |
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
| `image.digest` | `""` | Pins the image by digest (sha256:...), appended to the tag as tag@digest so the runtime pulls exactly that image whatever the tag resolves to later |
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
