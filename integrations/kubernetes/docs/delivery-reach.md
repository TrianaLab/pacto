# Delivery reference: appliers and several clusters

[Pacto delivery](delivery.md) never applies a version itself. It moves the
version through the manager that already applies the service's workloads:
a Flux Kustomization, a Flux HelmRelease or an Argo CD Application. This page
covers how the promoter finds that manager, the HelmRelease and Argo CD
set-ups, and a Stage that follows a Stage in another cluster.

!!! warning "Experimental"
    HelmRelease, Argo CD and several clusters are experimental, within an Alpha
    component: they may change more than the rest of delivery. Every pull
    request that changes delivery runs each of them live once; Flux
    Kustomizations run in every delivery scenario.

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

Marks of two different managers read as ambiguous. The Stage records the
result in `status.applier`, with the evidence, `asOf` and `completeness`, and
compares it with its `spec.applyRef`:

| State | Means | A promote |
|---|---|---|
| `Adopted` | The Stage's applier manages every workload | goes ahead |
| `Unmanaged` | No Pacto reports a workload yet, or the workload it reports does not exist yet | goes ahead |
| `Conflict` | Another manager or another object owns a workload, a plain Helm release owns it, or the workloads disagree | is refused; `Ready` is `False` with `ApplierConflict` |
| `Unknown` | A workload carries no mark of the Stage's applier, an unsupported or ambiguous manager owns it, Pacto could not read it, a Pacto is not reconciled yet or evaluated another workload than the Stage runs, or a Kustomization in another namespace does not apply from the Stage's OCIRepository | is refused; `Ready` is `False` with `ApplierUnknown` |

Nobody can accept a `Conflict` or an `Unknown`: Pacto never writes a pointer
another manager may overwrite, and moving a workload is yours to do.

- `pacto delivery status`, `pacto_delivery_status` and the dashboard print
  one line, such as
  `applier: Argo CD (Application payments, tracking annotation) (as of 2026-11-15T09:59:00Z)`,
  with the reason when the Stage does not adopt it and a warning when the
  promoter stopped checking.
- `pacto delivery plan` keeps what the promoter observed apart from what the
  Stage declares and what the contract says.
- A contract on pactoVersion 2.5 can require a manager with
  [`delivery.applier`](../../contract-reference/delivery.md).

## Apply through a Flux HelmRelease

A Stage can apply through a Flux `HelmRelease` whose `spec.chartRef` names an
`OCIRepository`. The promoter writes the chart digest to that repository's
`spec.ref.digest`, and a move counts as applied once the HelmRelease is
`Ready` at that digest. A failed release of the digest fails the promote and
starts the usual rollback.

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
  set no Kustomize images, patches or components, Helm parameters, values or
  value files, or plugin. Otherwise the Stage's `Mediated` condition is
  `False`: something besides the digest decides what runs.
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
to break-glass, so nobody else changes what those bytes become. Only a Flux
`OCIRepository` with `spec.verify` admits a new version, so the first Stage of
a service needs Flux's source controller in its cluster even when Argo CD
applies it. Stages after it can take versions from that Stage, in the same
cluster or through its records, with no Flux at all. An Argo CD Stage places no
consumers, so label the Pactos it runs with
`delivery.pacto.trianalab.io/environment`.

## Follow a Stage in another cluster

A Stage in one cluster can take its versions from a Stage in another cluster,
such as prod in one cluster following staging in another. The clusters do not
talk to each other. The downstream promoter reads the upstream Stage's signed
records from the registry.

You need:

1. The recorder running in the upstream cluster (see
   [Signed records](delivery-records.md)).
2. The upstream recorder's public key in the downstream promoter's trust
   config, bound to the upstream stream:

   ```yaml
   promoter:
     upstreamTrust: |
       keys:
         - name: staging-recorder
           publicKey: |
             -----BEGIN PUBLIC KEY-----
             ...
             -----END PUBLIC KEY-----
           streams: ["8d1e…/staging/*"]
   ```

3. The downstream Stage naming the upstream stream and the repository that
   holds its records. The stream is the upstream Stage's `status.stream.id`.

   ```yaml
   apiVersion: delivery.pacto.trianalab.io/v1alpha1
   kind: Stage
   metadata: {name: payments, namespace: prod}
   spec:
     service: {name: payments}
     environment: prod
     applyRef: {name: payments}
     candidates:
       upstreamRecords:
         repository: registry/delivery/streams
         stream: 8d1e…/staging/payments
   ```

### What the follower takes

The promoter rebuilds from the records what the upstream verified: a promote
that `Succeeded`, `Verified` and passed the published gate there is offered
here, and a failure the follower has read is never taken: a version that
failed there, or was rolled back after release. That includes a version that fails a check after release when
the promoter cannot roll it back there (a person or agent chose it, or its
rollback is used up): the promoter freezes that Stage with a freeze operation
whose record names the failed version. Its own gates, approvals and checks
still run. The digest must also exist in the Stage's own apply repository: use the same repository or a
mirror that keeps digests. `pacto delivery plan` reads these records only with
`--trust` and the upstream recorder's key.

### When the records cannot be trusted

The promoter offers nothing when it cannot check the history: without
`delivery.promoter.upstreamTrust` the Stage is not `Ready` (`UpstreamUntrusted`). A gap,
a fork or an unreadable registry make it not `Ready` (`UpstreamUnreadable`,
saying why) until a read succeeds, and refuse a promote as `NotPublished`. A
record no trusted key signed is skipped, since anyone with push access could
attach one. A trusted record that stops verifying leaves a gap.

What the follower cannot see, it cannot refuse. It detects records removed
from before `status.upstreamSeen`, but not a record hidden after it was
written and before the follower first read it, nor one signed with a key
`upstreamTrust` does not list, which is skipped like a forgery. A version that
failed upstream through such a record can still be taken. So rotate the
recorder's key in every follower first (list the new key beside the old one
in `upstreamTrust`), then upstream. The Stage's `Ready` message counts the
referrers on the upstream anchor it could not verify, so a skipped record
does not go unsaid.

### When records were removed or rewritten

The Stage remembers the newest upstream record it read in
`status.upstreamSeen`. A later read that ends before it, or holds another
record at its seq however far the stream has grown since, is refused the same
way: records were removed or rewritten, and one of them could be a failure.
Every `pacto.published` verdict on such a Stage cites the record it relied on
as `upstreamHead` and `upstreamSeq`, so an auditor can replay the stream up to
it.

Sometimes the cut is intended: the upstream stream was recreated, or a
`StreamConflict` repair removed its newest record. Nothing clears the mark on
its own, so the follower stays not `Ready` until a person does. Once an
auditor confirms the cut, clear it, with the right to patch `stages/status`.
Admission lets that right do nothing else: only the promoter writes the rest of
a Stage's or an Operation's status, so the write must remove `upstreamSeen` and
change nothing more. The next read, within 30 seconds, sets it again:

```bash
kubectl patch stage <stage> -n <namespace> --subresource=status --type=merge \
  -p '{"status":{"upstreamSeen":null}}'
```

The promoter reads the upstream registry at most every 30 seconds for
auto-promotion, and afresh when it plans a promote. It fetches and verifies
each record once.

### Freezes

A freeze is local, in one cluster or across two. Freezing the upstream Stage
does not stop a follower from taking a version the upstream already verified:
freeze the follower too. When the promoter freezes a Stage because a check
failed after release, its record names the failed version, so followers never
take that version.

## Limits

- A Stage sees the upstream's history only as far as its records reach the
  registry, so a failure not yet recorded is not seen.
- A cut is noticed only against a record the Stage already read. A Stage
  that starts following a stream whose newest records were already removed
  cannot tell.
