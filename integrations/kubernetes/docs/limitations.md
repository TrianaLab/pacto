# Limitations

The operator is deliberately read-only and conservative: it never fabricates a
violation from evidence it cannot trust. The boundaries below follow directly from
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
- **Active health probing (Tier A)** requires `--enable-probing`; otherwise health
  uses only passive readiness-probe and EndpointSlice signals (Tier B).
- **Interface name-match discovery** requires `--interface-name-match-discovery`
  and only ever assists positive availability -- it never produces an absent or
  error result.

See [Operator configuration](operator-configuration.md) for these flags.

!!! warning "The chart does not expose these flags"
    All three are controller command-line flags, and the Helm chart renders a
    fixed argument list with no `extraArgs` value. On the documented install
    path there is currently **no way to turn any of them on**: `helm template`
    the chart and the container's `args` contain none of them, and no value adds
    them. Enabling them today means editing the rendered Deployment yourself
    (which Helm will revert on the next `helm upgrade`), and metrics observation
    additionally needs the `metrics-observation-role` ClusterRole, which lives in
    the repository's kustomize overlay (`config/rbac/metrics-observation/`) and is
    not packaged in the chart either.

    Treat these as not-yet-available through Helm rather than as switches you can
    flip. They are reachable from a kustomize deployment of `config/`.

## `NotEvaluated` is reserved

`NotEvaluated` is a valid `contractStatus` enum value that the operator does not
currently emit. A valid, targeted contract with no runtime evidence yields
`Unknown`, not `NotEvaluated`. The value exists for parity with the engine
dashboard, which uses it for offline OCI or local sources that were never
runtime-evaluated.

## Stabilization delay

Confirmed runtime-drift violations only surface after the stabilization window
(`--stabilization-window`, default two minutes). This trades immediacy for
resistance to transient blips: a single negative observation reads `Unknown` until
the negative streak spans the whole window.

## API version

The CRDs are served at `v1alpha1`. Fields are added conservatively and
additively; see the [CRD reference](crd-reference.md) for the current schema.
