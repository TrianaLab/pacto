# Limitations

What the operator cannot see, what you must switch on yourself and where its
answers lag. The rule behind all of it: the operator never reports a violation
it cannot prove, and it never changes the workloads it watches.

## Read-only towards your workloads

The operator never modifies the workloads it observes: it does not restart pods,
edit Deployments or change anything it was pointed at. Observation is read and
report only. Deciding which version runs is [Pacto delivery](delivery.md), a
separate opt-in chart; installing the operator grants none of it.

It is not a read-only component overall. At chart defaults it manages the Pacto
dashboard for you, which means creating a Deployment, Service, ServiceAccount,
Secret, ClusterRole and ClusterRoleBinding of its own. The grants that allow that
are broad enough to escalate privilege. [RBAC](rbac.md) has the full rule
list and the flags that switch the managed components off.

## Observation boundaries

When the operator has no reliable way to check something, it reports
`Unsupported` instead of guessing. `Unsupported` makes the contract `Unknown`,
never `NonCompliant`. That happens for:

- **External or non-Pacto Services** -- a dependency backed by an `ExternalName`
  Service, or a Service the operator does not manage, cannot be reached reliably
  and is reported `Unsupported`.
- **Non-HTTP capability bindings** -- health and metrics probing supports HTTP
  bindings only; gRPC capability probing is not implemented and returns
  `Unsupported`.
- **Unbound interfaces and capabilities** -- the target port cannot be resolved
  and the result is `Unsupported`. That happens when an interface has no
  `interfaceBindings` entry (and name-match discovery is off), or when a
  capability's owning interface has no binding.

## Opt-in features

Some observation is off by default because it costs cluster calls or opens an
in-cluster request surface:

- **Metrics observation** requires `--enable-metrics-observation`; otherwise the
  metrics dimension returns `Unsupported`.
- **Active health probing** requires `--enable-probing`: the operator issues an
  in-cluster HTTP GET against the health capability's own port and path. Without
  it, health falls back to what the cluster already knows: an `httpGet`
  readiness probe on the container behind that port, plus a Ready EndpointSlice
  endpoint. That observes the workload rather than the declared endpoint. The
  flag help and the [observation reference](runtime-observations.md) call these
  two *Tier A* and *Tier B*; nothing the operator reports uses those labels.
- **Interface name-match discovery** requires `--interface-name-match-discovery`
  and only ever assists positive availability -- it never produces an absent or
  error result.

See [Operator configuration](operator-configuration.md) for these flags.

The chart exposes the first two as `controller.enableProbing` and
`controller.enableMetricsObservation`; the second also grants read access to
ServiceMonitors and PodMonitors. Name-match discovery has no chart value.

!!! warning "Name-match discovery is not a chart value"
    The chart renders a fixed argument list with no `extraArgs` value, so the
    only way to turn `--interface-name-match-discovery` on is to add it to the
    running Deployment yourself:

    ```bash
    kubectl patch deployment pacto-operator -n pacto-operator-system \
      --type=json \
      -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--interface-name-match-discovery"}]'
    ```

    **This does not survive `helm upgrade`.** Helm re-renders `args` and the
    flag disappears silently. Re-apply the patch after every upgrade.

    An operator installed without the chart needs its own grant for
    `--enable-metrics-observation`. Add a separate ClusterRole, never a patch of
    `manager-role`, whose rules are an atomic list a strategic merge would wipe:

    ```yaml
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRole
    metadata:
      name: metrics-observation-role
    rules:
      - apiGroups: ["monitoring.coreos.com"]
        resources: ["servicemonitors", "podmonitors"]
        verbs: ["get", "list", "watch"]
    ---
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: metrics-observation-rolebinding
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: metrics-observation-role
    subjects:
      - kind: ServiceAccount
        name: pacto-operator                  # the chart's ServiceAccount
        namespace: pacto-operator-system      # the release namespace
    ```

    The repository also carries a kustomize overlay at
    `config/rbac/metrics-observation/`, but `config/` is kubebuilder scaffolding:
    it is not published with a release, and no test or CI job deploys from it.
    It is a source of the YAML above, not a supported install path.

## Runtime conformance boundaries

The [node agent and the complementary checks](runtime-conformance.md) never
turn what they cannot see into a violation. These things stay out of reach:

- **Visibility now defaults to internal.** An interface with no `visibility`
  is `internal`, so one served through a `LoadBalancer` Service or an Ingress
  that publishes a public address reports `INTERFACE_VISIBILITY_MISMATCH` after
  upgrading, node agent or not. Declare `visibility: public` for it.
- **`hostNetwork` pods** share the node's network namespace, so their sockets
  cannot be attributed. With the agent on, their interfaces read `Insufficient`
  unless another backing pod is seen listening.
- **Loopback-only listeners** do not serve the pod IP and do not count as
  serving the port.
- **TCP only.** UDP sockets are not observed, so a UDP or SCTP Service port
  gets no kernel opinion; the Kubernetes result stands.
- **Connection history.** A dependency is judged from the latest success and
  failure within the staleness bound. A dependency the workload has not called
  recently has no kernel opinion, and a recent success never overrides a
  provider with no ready endpoint; the two together read `Insufficient`.
- **TLS** hides the protocol inside, so a TLS port leaves `interfaces[].type`
  unverified, and `asyncapi` names no wire protocol to check.
- **Socket-level load balancing** (for example a kube-proxy replacement)
  connects to the backend pod directly. The operator matches every current
  endpoint IP too, so a connection to a backend that has since left the
  EndpointSlice no longer counts.
- **Exposure** is `external` only when a Service or Ingress status shows a
  public address ([how exposure is decided](runtime-conformance.md#how-exposure-is-decided)).
  The operator does not read node or gateway addresses, so a `NodePort`
  Service or an HTTPRoute leaves exposure unknown. A route outside its watch
  scope, another route kind or a mesh gateway is not seen, so `internal` can
  miss an exposure; it never invents one.
- **Kernel requirements.** A node without BTF (the kernel type information
  the agent needs) reports `Failed`, which turns its pods' interface checks
  into `COLLECTION_FAILED`.
- **Docker Compose and bare hosts.** The node agent is the Kubernetes way to
  run `pacto agent`. On a Docker host or a server, run
  [`pacto agent`](../../runtime-agent.md) yourself; the cluster-only checks
  then stay unverified ([what stays unverified off Kubernetes](../../runtime-conformance.md#what-stays-unverified-off-kubernetes)).
- **Private registries.** The agent's ServiceAccount carries the chart's
  `imagePullSecrets`, which must also exist in the agent namespace
  (`nodeAgent.namespace`): a pull secret is read from the pod's own namespace.
  An operator installed without the chart passes
  `--node-agent-image-pull-secret` itself.

## Node agent heartbeat and identity

- **Heartbeat.** Every publish interval the agent renews a
  `coordination.k8s.io` Lease named after its node, held by the node and valid
  for three intervals. It rewrites its `NodeRuntimeObservation` only when the
  view changed (`lastPublished` aside) or every tenth interval, and renews the
  Lease only after the view is written, so a fresh Lease never vouches for a
  stale view. The operator takes the heartbeat from the Lease `renewTime`, or
  from `lastPublished` when there is no Lease. An observation deleted by hand
  on a quiet node comes back within ten intervals; until then the node reads
  stale.
- **One node, one writer.** RBAC cannot say which node's objects an agent may
  write. Where the API serves `ValidatingAdmissionPolicy`, the chart adds one
  that lets the agent's ServiceAccount create or update a
  `NodeRuntimeObservation` or Lease only under the node name its token carries
  (`authentication.kubernetes.io/node-name`). A token without that name is
  refused, never trusted: the node then reads stale, not compliant.
- **Kubernetes 1.30 or later.** Tokens carry the node name from 1.30, the
  release `ValidatingAdmissionPolicy` became generally available. On an older
  cluster the policy is not rendered and RBAC alone lets any agent write any
  node's object in the agent namespace.

## `NotEvaluated` is reserved

`NotEvaluated` is a valid `contractStatus` enum value that the operator does not
currently emit. A valid, targeted contract with no runtime evidence yields
`Unknown`, not `NotEvaluated`. The value exists for parity with the engine
dashboard, which uses it for offline OCI or local sources that were never
runtime-evaluated.

## Stabilization delay

The stabilization window
([`--stabilization-window`](operator-configuration.md), default two minutes)
trades immediacy for resistance to transient blips: a single negative observation
reads `Unknown` until the negative streak spans the whole window.

It applies to **absences only** — something the operator expected to find and
did not:

- `INTERFACE_ABSENT`
- `DEPENDENCY_UNREACHABLE`
- `CAPABILITY_ABSENT` (active probing only)
- `CONFIGURATION_ABSENT`, for a declared Secret or ConfigMap

A **mismatch** — something that is there and contradicts the contract — is
`NonCompliant` on the first check that observes it, with no delay:
`WORKLOAD_MISMATCH`, `PERSISTENCE_MISMATCH`, `CONFIGURATION_MISMATCH`,
`INTERFACE_PROTOCOL_MISMATCH`, `INTERFACE_VISIBILITY_MISMATCH` and
`DEPENDENCY_VERSION_MISMATCH`. There
is nothing transient to wait out; the evidence is already conclusive.

This split matters if you gate deployments on the verdict. See
[GitOps promotion gates](gitops.md#timing).

## Delivery and private registries

Images can stay private with only a pull Secret. A private contract or deploy
repository needs a login in three places, or the checks that read it answer
Unknown, every change waits for a person to accept the unknown and an accepted
unknown never moves on to the next environment:

- delivery: set `delivery.promoter.registryCredentials.secretName` on the
  operator chart to a `kubernetes.io/dockerconfigjson` Secret in the release
  namespace;
- Flux: `secretRef` on each OCIRepository in `delivery/<env>.yaml`;
- the operator: `spec.contractRef.pullSecretRef` in `delivery/pacto.yaml`.

`pacto init --delivery --registry-secret <name>` writes the last two, naming a
`kubernetes.io/dockerconfigjson` Secret you create in each environment's
namespace; the chart value stays yours to set. See
[Pactify a service](../../pactify.md#4-connect-the-service-to-your-cluster) for
the commands.

## API version

The CRDs are served at `v1alpha1`. Fields are added conservatively and
additively; see the [CRD reference](crd-reference.md) for the current schema.
