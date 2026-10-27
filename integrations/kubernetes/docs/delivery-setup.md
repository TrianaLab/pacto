# Delivery reference: setup and gates

Reference for [Pacto delivery](delivery.md). To pactify a service end to end,
follow [Pactify a service](../../pactify.md) instead. The API group is
`delivery.pacto.trianalab.io/v1alpha1` (alpha).
Plans, approvals and who may do what are in
[Delivery reference: plan, propose and approve](delivery-operations.md).

## Install

Delivery is the `pacto-operator` chart's `delivery` section, off by default. It needs Flux (source and kustomize controllers). Turn it on:

--8<-- "integrations/kubernetes/docs/generated/_delivery-install-command.md"

Replace `staff` with the group your people are in: anyone outside it counts as
an agent, which may propose but never approve. The chart refuses an empty
`humanGroups` unless you set `delivery.identities.allowEmptyHumanGroups=true`
(see [identities](delivery-identity.md)).

This needs Helm 3.14 or later. Delivery runs in `delivery.namespace`
(`pacto-delivery`, never the release or node agent namespace), where the operator holds no
role, so it cannot act as the promoter. It still reads every Secret: prefer
KMS or keyless recorder signing. Set `delivery.createNamespace=false` to
create the namespace, with the recorder key, yourself. Bind
`pacto-operator-delivery-agent` to agents and
`pacto-operator-delivery-approver` to approvers. The image is
[signed](artifact-hub.md).

The admission policies make the actor of an Operation and the person behind
an Approval true, so the chart refuses Kubernetes below 1.30, a cluster
without the `ValidatingAdmissionPolicy` API and
`delivery.admissionPolicies.enabled=false`, unless you set
`delivery.admissionPolicies.allowUnguarded=true`. With `helm template`, pass
`--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy`.
The promoter checks every policy the chart rendered. While one is missing,
does not fail closed, has a binding that does not deny, binds another policy
or is narrowed by `matchResources` or `paramRef`, it admits no operation and
counts no approval, and shows `Unguarded`.

To run an unreleased build, from the repository root:

```bash
docker build -f integrations/kubernetes/Dockerfile --target delivery --build-arg CMD=cmd/delivery \
  -t registry.example.com/pacto-delivery:dev .
helm upgrade --install pacto-operator integrations/kubernetes/charts/pacto-operator \
  -n pacto-operator-system --create-namespace --set delivery.enabled=true \
  --set delivery.image.registry=registry.example.com --set delivery.image.repository=pacto-delivery \
  --set delivery.image.tag=dev
```

## Metrics and Events

The promoter and the recorder serve Prometheus metrics over plain HTTP on
port 8080. The promoter's are behind the `<release>-delivery-metrics` Service; set
`delivery.metrics.serviceMonitor.enabled=true` to create a Prometheus Operator
ServiceMonitor for it. `delivery.recorder.monitoring.enabled=true` gives the
recorder's their own Service, ServiceMonitor and
[alerts](delivery-records.md).

| Metric | What it counts |
|--------|----------------|
| `pacto_delivery_operations_total` | concluded operations by `type`, `outcome`, `verification` and `actor_class`: how much shipped with no person, and how often a person accepted an unknown |
| `pacto_delivery_verdicts_total` | gate verdicts of concluded operations and re-checks, by `gate`, `phase` and `result` |
| `pacto_delivery_stage_frozen` | 1 while a Stage is frozen |
| `pacto_delivery_stage_watched` | 0 while the newest re-check of the running version did not pass |
| `pacto_delivery_records_pending` | concluded operations per stream that have no record yet (recorder) |

The promoter emits a Kubernetes Event on an operation when it waits on a person
(`Held`) and when it concludes (its outcome). It emits one on a Stage when it
raises a rollback (`RollbackRaised`), freezes (`Frozen`) or stops being watched
(`RecheckUnknown`, `RecheckFailed`).

## Upgrading

[Upgrade the operator](upgrade.md) as usual: `helm upgrade` updates the
delivery CRDs, and `helm.sh/resource-policy: keep` keeps them and their history
on uninstall.

## How a change moves

A **Stage** names the Flux `OCIRepository` that applies it, and its
`status.pointer` is the digest it moved the environment to. Only the promoter
moves it, and the only field it writes on an object it does not own is that
repository's `spec.ref.digest`. Every change is an **Operation** whose spec
never changes:

```mermaid
flowchart LR
  P[Pending] -->|gates pass| L[lock]
  P -->|a person must decide| H[Held]
  H -->|approved| L
  L --> R[Running]
  R -->|Flux applied it| V[Verifying]
  V -->|Pass| S[Succeeded]
  V -->|Fail| F[Failed, rolled back]
  S -->|re-check fails| F
```

- Before: `pacto.published` admits only a version
  [`pacto publish`](../../publishing.md) published, `pacto.diff` compares the
  contracts and `pacto.impact` checks every consumer in the environment that
  declares a range on the service, as
  [Who is asked and why](delivery.md#who-is-asked-and-why) explains. Anything
  unseen (an unreadable Pacto, a registry silent for 10 seconds) is
  [Unknown](delivery.md#what-unknown-means).
  `pacto.suites` holds a version that does not ship a suite one of the Stage's
  checks names, for a person to accept the unknown. It also holds a version
  that changes a suite it ships, on every level.
- After: the Pacto contract check counts only when
  `status.evaluatedRevision` names this version's contract digest and the
  current workload generation. `Compliant` and `Warning` pass, `NonCompliant`
  and `Invalid` fail, anything else is Unknown.
  [Health checks](delivery-health.md) can join it.
- Rollbacks, freezes and the 50 failed versions never tried again work as
  [What happens to a change](delivery.md#what-happens-to-a-change) says. A
  rollback that is refused or unclear also freezes the Stage, and a person's
  rollback puts an [auto-hold](../../glossary.md#auto-hold) on a Stage on an
  automatic level.

Only six built-in operation types exist: promote, rollback, override, freeze,
unfreeze and autonomy, which moves the Stage up or down
[the autonomy ladder](../../autonomy.md). Stage approval policy is
`approval.always`, not an expression.

## What the pointer guarantees

Pacto mediates the pointer and signed records name it. The pointer alone
decides what runs only when nothing else can change it; the Stage's `Mediated`
condition says whether that holds. It is `True` when exactly one
Flux Kustomization applies the apply `OCIRepository`, both carry the
`delivery.pacto.trianalab.io/apply-ref` label, the Kustomization sets a
`serviceAccountName` and sets none of `images`,
`patches`, `components`, `postBuild`, `namePrefix`, `nameSuffix`, `kubeConfig`,
`suspend` or `decryption`, and the `OCIRepository` sets no `spec.ignore`. Without a
`serviceAccountName` Flux applies each version as kustomize-controller, which
is cluster-admin in a default install, so a version whose contract did not
change could ship a ClusterRoleBinding and an automatic level would apply it:
the levels judge only the contract. `pacto init --delivery`
writes a namespaced deployer account for each environment. On
Argo CD it is `True` when the Application carries the label, has one source and sets no
Kustomize images, patches or components, Helm parameters, values or value
files, or plugin. Otherwise it is `False` and the reason
(`ApplyRefUnguarded`, `NoApplier`, `SeveralAppliers`, `ApplierUnguarded`,
`ApplierUnscoped`, `ApplierOverrides`, `SeveralSources`) names what else decides what runs.

### What only break-glass may change

On labelled objects, only break-glass may change: removing
the label, the `OCIRepository`'s `spec.url`, `spec.layerSelector` and
`spec.ignore`, the rest of an Application's `spec.source`, its `spec.sources`,
`spec.destination` and `spec.project`, and a Kustomization's
`sourceRef`, `path`, `images`, `patches`, `components`, `postBuild`,
`namePrefix`, `nameSuffix`, `targetNamespace`, `serviceAccountName`,
`kubeConfig`, `suspend` and `decryption`. A GitOps tool that applies
those objects must leave these fields alone or run as break-glass.
Deleting a labelled object, or creating one with the label, is an edit too:
only break-glass, `system:masters`, the namespace controller, the garbage
collector and the users in `delivery.admissionPolicies.gitopsUsers` (Flux's
kustomize-controller and Argo CD's application controller by default) may.
On kubeadm clusters, kind included, the admin kubeconfig is in
`kubeadm:cluster-admins`, not `system:masters`, so let GitOps create the
pointers or add that user to `gitopsUsers`.
If one of them is ever recreated without the label, the Stage shows `Ready`
`False` with the reason `ApplierUnlabelled` and `Mediated` `False` with the
reason `ApplyRefUnguarded`.
`kubectl set image` on the workload itself is not mediated: Flux puts it back
on its next reconcile, and Argo CD does with `selfHeal`; until then it runs
beside the pointer.

## Set up a Stage

The candidate `OCIRepository` follows the deploy artifact's repository and its
`spec.verify` names the publisher, so Flux checks every signature. The Stage
lists each digest Flux verified in `status.published` and refuses anything else
as `NotPublished`. Without a `secretRef` or `matchOIDCIdentity` Flux accepts a
keyless signature from anyone, so such a Stage promotes nothing. Flux reads a
`matchOIDCIdentity` issuer and subject as regular expressions, so each must be
anchored (`^...$`) and must not match an identity nobody has: `.*`, or a
subject left open at the end, counts as no identity at all. A Stage that
cannot promote has `Ready` `False` with the reason `CandidateUnverified`,
`CandidateNotFound`, `NoCandidateSource` or `UpstreamUntrusted`.

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: payments-candidates, namespace: test}
spec:
  url: oci://registry/apps/payments
  ref: {semver: ">=0.0.0"}
  verify: {provider: cosign, secretRef: {name: cosign-pub}}
  # keyless: matchOIDCIdentity with the issuer and subject of publish.sign
```

Label the `OCIRepository` that applies the Stage with
`delivery.pacto.trianalab.io/apply-ref=<stage name>`, so admission keeps
everyone but the promoter and break-glass off its digest, and give it the same
`spec.verify`. The value names the one Stage that applies through it, so a
second Stage cannot drive the same pointer. Without the label, or with another
Stage's name in it, the promoter may not write the digest either: the Stage has
`Ready` `False` with the reason `ApplierUnlabelled`, and a refused write shows
in the operation's `status.message`. A downstream Stage takes only what its upstream lists in
`status.offered`: versions that passed the published gate and were verified
there, never one let through on an accepted unknown.

```yaml
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: payments, namespace: prod}
spec:
  service: {name: payments}
  environment: prod
  applyRef: {name: payments}
  candidates:
    upstreamStage: {name: payments, namespace: test}
  startLevel: advise
  gates:
    verify: {window: 5m}
```
