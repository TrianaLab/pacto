# Delivery reference: appliers

[Pacto delivery](delivery.md) never applies a version itself. It moves the
version through the manager that already applies the service's workloads:
a Flux Kustomization, a Flux HelmRelease or an Argo CD Application. This page
covers finding that manager and the HelmRelease and Argo CD set-ups. Members,
and Stages that follow a Stage in another cluster, are covered in
[Several clusters](delivery-members.md).

## Which manager applies a Stage

The promoter reads, and never writes, the workloads each Pacto in the Stage's
namespace reports for the service, and names the manager of each one. The
first match wins:

1. An `argocd.argoproj.io/tracking-id` annotation that names this object:
   Argo CD, with the Application from the id.
2. `helm.toolkit.fluxcd.io/name` and `/namespace` labels: a Flux HelmRelease.
3. `kustomize.toolkit.fluxcd.io/name` and `/namespace` labels: a Flux
   Kustomization.
4. `meta.helm.sh/release-name` and `/release-namespace` annotations: a plain
   Helm release.
5. An `app.kubernetes.io/instance` label written by an `argocd` field manager:
   Argo CD.
6. A controller owner reference, kapp, Rancher Fleet, Spinnaker, Skaffold,
   Terraform or Pulumi: a manager Pacto does not support.
7. A field manager, label or annotation the chart's `fingerprints.managers`
   names: `external: <product>`. It weighs like the first four marks, so it
   and another one read as ambiguous. See
   [Fingerprints](../../operational-graph-providers.md#fingerprints).

Marks of two different managers read as ambiguous. The Stage records the
result in `status.applier`, with the evidence, `asOf` and `completeness`, and
compares it with its `spec.applyRef`:

| State | Means | A promote |
|---|---|---|
| `Adopted` | The Stage's applier manages every workload | goes ahead |
| `Unmanaged` | No Pacto reports a workload yet, or the workload it reports does not exist yet | goes ahead |
| `Conflict` | Another manager or another object owns a workload, a plain Helm release owns it, or the workloads disagree | is refused; `Ready` is `False` with `ApplierConflict` |
| `Unknown` | A workload carries no mark of the Stage's applier, an unsupported, external or ambiguous manager owns it, Pacto could not read it, a Pacto is not reconciled yet or evaluated another workload than the Stage runs, or a Kustomization in another namespace does not apply from the Stage's OCIRepository | is refused; `Ready` is `False` with `ApplierUnknown` |

Nobody can accept a `Conflict` or an `Unknown`: Pacto never writes a pointer
another manager may overwrite, and moving a workload is yours to do. The same
marks decide where Pacto creates the Stages of the
[installation ladder](delivery-environments.md).

- `pacto delivery status`, `pacto_delivery_status` and the dashboard print
  one line, such as
  `applier: Argo CD (Application payments, tracking annotation) (as of 2026-11-15T09:59:00Z)`,
  with the reason when the Stage does not adopt it and a warning when the
  promoter stopped checking.
- `pacto delivery plan` keeps what the promoter observed apart from what the
  Stage declares.
- The Stage's `spec.applyRef` names the applier; a contract never names one.
- An `external: <product>` workload is gate-only: the verdict reads
  `external: <product> manages <workload> (<evidence>): Pacto runs the gates and writes no pointer`.

## Apply through a Flux HelmRelease

A Stage can apply through a Flux `HelmRelease` whose `spec.chartRef` names an
`OCIRepository`. The promoter writes the chart digest to that repository's
`spec.ref.digest`, and a move counts as applied once the HelmRelease is
`Ready` at that digest. A failed release fails the promote and starts the
usual rollback.

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: payments-chart
  namespace: prod
  labels: {delivery.pacto.trianalab.io/apply-ref: payments}
spec:
  url: oci://registry/charts/payments
  interval: 1m
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: payments
  namespace: prod
  labels: {delivery.pacto.trianalab.io/apply-ref: payments}
spec:
  interval: 1m
  serviceAccountName: payments-deployer
  chartRef: {kind: OCIRepository, name: payments-chart}
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: payments, namespace: prod}
spec:
  service: {name: payments}
  environment: prod
  applyRef: {kind: HelmRelease, name: payments}
  candidates:
    upstreamStage: {name: payments, namespace: test}
```

- Both objects carry the `apply-ref` label set to the Stage's name, and the
  chart repository lives in the Stage's namespace. A `chartRef` to another
  namespace reads `ApplierUnreadable`, and the promoter never writes to it.
- A HelmRelease that takes its chart from `spec.chart`, a HelmChart, reads
  `ApplierNotChartRef`: a HelmChart pins a version, never a digest.
- `values`, `valuesFrom` or `postRenderers` set the Stage's `Mediated`
  condition `False` (`ApplierOverrides`), and no `serviceAccountName` sets it
  `False` (`ApplierUnscoped`). The message names the fields, never their
  values.
- An admission policy keeps the chart source, values, service account and
  `suspend` of a labelled HelmRelease to break-glass.
- The chart grants HelmRelease read when Flux access is on and the cluster
  serves `helm.toolkit.fluxcd.io/v2` HelmRelease.

## Apply through Argo CD

A Stage can point at an Argo CD `Application` instead of a Flux
`OCIRepository`. The chart grants the promoter access by itself: under the
default `delivery.argocd.enabled: auto` it does when the cluster serves Argo CD
Applications at install or upgrade, so install Argo CD first or run
`helm upgrade` after. Set `true` or `false` to decide regardless of the API.

This gives the promoter `patch` on Argo CD Applications and adds an admission
policy. On a labelled Application only the promoter or break-glass may change
`spec.source.targetRevision`.

The Application must:

- live in the Stage's namespace. Argo CD calls this
  [Applications in any namespace](https://argo-cd.readthedocs.io/en/stable/operator-manual/app-any-namespace/).
  Add the namespace to `application.namespaces` and to the project's
  `sourceNamespaces`. An Application of the same name in the Argo CD
  namespace is a different object, so its workloads read `Conflict`.
- have one OCI source (`spec.source.repoURL: oci://…`), not `spec.sources`, and
  set no Kustomize images, patches or components, Helm parameters or values,
  or plugin. Its only value file may be `values-<environment>.yaml` for the
  Stage's `spec.environment`; see
  [Values per environment](delivery-environments.md#values-per-environment).
  Otherwise the Stage's `Mediated` condition is `False`: something besides
  the digest decides what runs.
- sync on its own (`syncPolicy.automated`), because the promoter never runs a sync.
- carry the `delivery.pacto.trianalab.io/apply-ref` label, set to the Stage's
  name. Without it the Stage has `Ready` `False` with the reason
  `ApplierUnlabelled`.
- start with `targetRevision` empty or at the digest the Stage runs. Any other
  value is a write the promoter did not make, so the first promote freezes the
  Stage.

The Stage is not `Ready` while the Application breaks one of these, and the
reason names which: `ApplierUnreadable` (missing, the promoter may not read
it, or its CRD is not installed, such as Argo CD in a Flux-only cluster),
`ApplierUnlabelled`, `ApplierMultiSource`, `ApplierNotOCI` or
`ApplierNotAutomated`. A Flux `OCIRepository` the Stage applies through needs
the same label, so `ApplierUnreadable` and `ApplierUnlabelled` apply to it too.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: payments
  namespace: prod
  labels: {delivery.pacto.trianalab.io/apply-ref: payments}
spec:
  project: default
  source:
    repoURL: oci://registry/deploy/payments
    path: .
  destination: {server: https://kubernetes.default.svc, namespace: prod}
  syncPolicy: {automated: {}}
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: payments, namespace: prod}
spec:
  service: {name: payments}
  environment: prod
  applyRef: {kind: Application, name: payments}
  candidates:
    upstreamStage: {name: payments, namespace: test}
```

### How a promote applies

The promoter sets `spec.source.targetRevision` to the new digest and asks
Argo CD to refresh. The move counts as applied once the Application is `Synced`
and `Healthy` at that digest; `status.sync.revision` alone is not proof, since
it moves on refresh while the old version still runs. A sync of the digest that
ends `Failed` or `Error`, or `Degraded` health after that sync, fails the
promote and starts the usual rollback.

Argo CD does not check signatures. The candidate source checks them, and the
promoter only ever writes a digest, so Argo CD fetches exactly the checked
bytes. Admission keeps the rest of `spec.source`, `spec.sources` and the label
to break-glass, so nobody else changes what those bytes become. The first
Stage of a service admits a new version through `spec.candidates.registry`,
where the promoter lists the service's version tags and verifies each
signature against the installation's publisher rule (`delivery.publishers`),
or through a Flux `OCIRepository` with `spec.verify`. A cluster that runs only
Argo CD uses `candidates.registry` and needs no Flux. Stages after it can take
versions from that Stage, in the same cluster or through its records. An Argo
CD Stage places no
consumers, so label the Pactos it runs with
`delivery.pacto.trianalab.io/environment`.
