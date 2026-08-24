# Limitations

The operator is deliberately conservative: it never fabricates a violation from
evidence it cannot trust, and it never writes to the workloads it observes. It is
not a read-only component overall — see below. The boundaries here follow from
that stance.

## Read-only towards your workloads

The operator never modifies the workloads it observes: it does not restart pods,
edit Deployments or change anything it was pointed at. Observation is read and
report only.

It is not a read-only component overall. At chart defaults it manages the Pacto
dashboard for you, which means creating a Deployment, Service, ServiceAccount,
Secret, ClusterRole and ClusterRoleBinding of its own — and the grants that allow
that are broad enough to escalate privilege. [RBAC](rbac.md) has the full rule
list and the flags that switch the managed components off.

## Observation boundaries

Several dimensions resolve to `Unsupported` (which reads as `Unknown`, never
`NonCompliant`) rather than guess:

- **External or non-Pacto Services** -- a dependency backed by an `ExternalName`
  Service, or a Service the operator does not manage, cannot be reached reliably
  and is reported `Unsupported`.
- **Non-HTTP capability bindings** -- health and metrics probing supports HTTP
  bindings only; gRPC capability probing is not implemented and returns
  `Unsupported`.
- **Unbound interfaces and capabilities** -- when an interface has no
  `interfaceBindings` entry (and name-match discovery is off), or a capability's
  owning interface has no binding, the target port cannot be resolved and the
  result is `Unsupported`.

## Opt-in features

Some observation is off by default because it costs cluster calls or opens an
in-cluster request surface:

- **Metrics observation** requires `--enable-metrics-observation`; otherwise the
  metrics dimension returns `Unsupported`.
- **Active health probing** requires `--enable-probing`: the operator issues an
  in-cluster HTTP GET against the health capability's own port and path. Without
  it, health falls back to what the cluster already knows -- an `httpGet`
  readiness probe on the container behind that port, plus a Ready EndpointSlice
  endpoint -- which observes the workload rather than the declared endpoint. The
  flag help and the [observation reference](runtime-observations.md) call these
  two *Tier A* and *Tier B*; nothing the operator reports uses those labels.
- **Interface name-match discovery** requires `--interface-name-match-discovery`
  and only ever assists positive availability -- it never produces an absent or
  error result.

See [Operator configuration](operator-configuration.md) for these flags.

!!! warning "The chart does not expose these flags"
    All three are controller command-line flags, and the Helm chart renders a
    fixed argument list with no `extraArgs` value. On the documented install
    path there is currently **no way to turn any of them on**: `helm template`
    the chart and the container's `args` contain none of them, and no value adds
    them. Treat these as not-yet-available through Helm rather than as switches
    you can flip.

    The only way to turn one on today is to add the flag to the running
    Deployment yourself, accepting that it is not managed state:

    ```bash
    kubectl patch deployment pacto-operator -n pacto-operator-system \
      --type=json \
      -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--enable-probing"}]'
    ```

    **This does not survive `helm upgrade`.** Helm re-renders `args` from the
    template and your addition disappears, silently — the operator comes back
    with the feature off and nothing reports that it changed. Re-apply the patch
    after every upgrade, or do not rely on the feature yet.

    Metrics observation needs one more thing: the operator's ServiceAccount has
    no read access to `monitoring.coreos.com`, so `--enable-metrics-observation`
    alone changes nothing. Grant it alongside the chart's own RBAC — a separate
    ClusterRole, never a patch of `manager-role`, whose rules are an atomic list
    a strategic merge would wipe:

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

## `NotEvaluated` is reserved

`NotEvaluated` is a valid `contractStatus` enum value that the operator does not
currently emit. A valid, targeted contract with no runtime evidence yields
`Unknown`, not `NotEvaluated`. The value exists for parity with the engine
dashboard, which uses it for offline OCI or local sources that were never
runtime-evaluated.

## Stabilization delay

Confirmed runtime-drift violations only surface after the stabilization window
([`--stabilization-window`](operator-configuration.md), default two minutes).
This trades immediacy for
resistance to transient blips: a single negative observation reads `Unknown` until
the negative streak spans the whole window.

## API version

The CRDs are served at `v1alpha1`. Fields are added conservatively and
additively; see the [CRD reference](crd-reference.md) for the current schema.
