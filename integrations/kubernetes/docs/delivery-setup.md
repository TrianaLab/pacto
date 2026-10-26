# Delivery reference: setup and gates

Reference for [Pacto delivery](delivery.md). To pactify a service end to end,
follow [Pactify a service](../../pactify.md) instead. The API group is
`delivery.pacto.trianalab.io/v1alpha1` (alpha).

## Install

Delivery is the `pacto-operator` chart's `delivery` section, off by default. It needs Flux (source and kustomize controllers). Turn it on:

--8<-- "integrations/kubernetes/docs/generated/_delivery-install-command.md"

This needs Helm 3.14 or later. Delivery runs in `delivery.namespace`
(`pacto-delivery`, never the release or node agent namespace), where the operator holds no
role, so it cannot act as the promoter. It still reads every Secret: prefer
KMS or keyless recorder signing. The chart refuses an Ed25519 key in a Secret
unless `delivery.recorder.signing.allowOperatorReadableKey: true` accepts that. Set `delivery.createNamespace=false` to
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
  --set delivery.image.repository=registry.example.com/pacto-delivery --set delivery.image.tag=dev
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
  declares a range on the service. A broken consumer holds the change for its
  owners; anything unseen (an unreadable Pacto, a registry silent for 10
  seconds) holds it for a person to accept the unknown. Pacto sees only
  consumers with a contract, so a breaking change that no Pacto depends on also
  waits for a person, unless the Stage sets
  `gates.impact.declaredConsumersOnly: true` to say every consumer has one.
  `pacto.suites` holds a version that does not ship a suite one of the Stage's
  checks names, for a person to accept the unknown. It also holds any version
  that adds, removes or edits a suite it ships, on every rung: a version never
  grades its own exam.
- After: the Pacto contract check counts only when
  `status.evaluatedRevision` names this version's contract digest and the
  current workload generation. `Compliant` and `Warning` pass, `NonCompliant`
  and `Invalid` fail, anything else is Unknown.
  [Health checks](delivery-health.md) can join it.
- None of a Stage's last 50 failed versions is promoted again there. The
  promoter raises one rollback per failure; if that fails, is refused or is
  unclear, or the version before failed too, the Stage freezes until a person
  unfreezes it. A person's rollback puts an auto-hold on a Stage on an
  automatic rung, so automation does not undo it.

Only six built-in operation types exist: promote, rollback, override, freeze,
unfreeze and autonomy, which moves the Stage up or down
[the autonomy ladder](../../autonomy.md). The promoter freezes a Stage when a
check after release fails and it cannot roll back. Stage approval policy is
`approval.always`, not an expression.

## What the pointer guarantees

Pacto mediates the pointer and signed records name it. The pointer alone
decides what runs only when nothing else can change it; the Stage's `Mediated`
condition says whether that holds. It is `True` when exactly one
Flux Kustomization applies the apply `OCIRepository`, both carry the
`delivery.pacto.trianalab.io/apply-ref` label and sets none of `images`,
`patches`, `components`, `postBuild`, `namePrefix` or `nameSuffix`. On
Argo CD it is `True` when the Application carries the label, has one source and sets no
Kustomize images, patches or components, Helm parameters, values or value
files, or plugin. Otherwise it is `False` and the reason
(`ApplyRefUnguarded`, `NoApplier`, `SeveralAppliers`, `ApplierUnguarded`,
`ApplierOverrides`, `SeveralSources`) names what else decides what runs.

On labelled objects, only break-glass may change: removing
the label, the `OCIRepository`'s `spec.url` and `spec.layerSelector`, the rest
of an Application's `spec.source` and its `spec.sources`, and a Kustomization's
`sourceRef`, `path`, `images`, `patches`, `components`, `postBuild`,
`namePrefix`, `nameSuffix` and `targetNamespace`. A GitOps tool that applies
those objects must leave these fields alone or run as break-glass.
Admission guards updates only. Whoever may delete and create these objects can
recreate one without the label; the Stage then shows `Ready` `False` with the reason `ApplierUnlabelled` and
`Mediated` `False` with the reason `ApplyRefUnguarded`. Keep delete and create
on them to the people you would put in break-glass.
`kubectl set image` on the workload itself is not mediated: Flux puts it back
on its next reconcile, and Argo CD does with `selfHeal`; until then it runs
beside the pointer.

## Set up a Stage

The candidate `OCIRepository` follows the deploy artifact's repository and its
`spec.verify` names the publisher, so Flux checks every signature. The Stage
lists each digest Flux verified in `status.published` and refuses anything else
as `NotPublished`. Without a `secretRef` or `matchOIDCIdentity` Flux accepts a
keyless signature from anyone, so such a Stage promotes nothing. A Stage that
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
`delivery.pacto.trianalab.io/apply-ref`, so admission keeps everyone but the
promoter and break-glass off its digest, and give it the same `spec.verify`.
Without the label the promoter may not write the digest either: the Stage has
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
  startRung: advise
  gates:
    verify: {window: 5m}
```

## Plan, propose and approve

```bash
pacto delivery plan -n prod --stage payments --to sha256:...
pacto delivery propose -n prod --stage payments --to sha256:... --intent-digest sha256:...
PACTO_DELIVERY_OIDC_ISSUER_URL=https://idp.example.com \
  pacto delivery approve -n prod <operation> --decision approve
```

`approve` never uses your kubeconfig: it makes you log in again in your
browser through its own `pacto-approve` client, so it needs the issuer the
cluster trusts (`--oidc-issuer-url` or `PACTO_DELIVERY_OIDC_ISSUER_URL`). See
[approve from the terminal](delivery-identity.md#approve-from-the-terminal).

`plan` predicts each gate's verdict and the one question a person would be
asked. `propose` with the plan's intent digest refuses with `PlanStale` if
anything moved since. `cancel` works until the pointer moves; after that,
propose `--type rollback --undoes <operation>`. A tag is refused. An agent may
cancel only an operation it proposed; a rollback or freeze the promoter raised,
or anyone else's operation, is cancelled by a person or break-glass.

`pacto mcp --delivery` gives agents the read, plan, history, blame, undo and
record-verify tools, plus propose and cancel only when the apiserver says the
caller is an agent. There is no approve tool.

## When another service must move first

When a promote breaks a consumer, the plan names a version of that consumer to
ship first: one that accepts both the running and the new provider version,
fits the other services there, is newer than what runs and was offered by its
upstream Stage. It shows in `plan`, `explain`, `status.plan.order` and the
dashboard's **Update these first**. While a change waits, the promoter plans
again, so a consumer that moved stops being asked and one newly broken is
asked. Pacto never promotes the other service itself.

Only consumers in the same environment count. A Pacto's
`delivery.pacto.trianalab.io/environment` label places it; without one, it is
where a Stage's Kustomization applies (`targetNamespace`), even if you deployed
it there for another environment, so label it then. A Pacto that cannot be
placed counts everywhere and, as a consumer, asks a person.

## Who may do what

| Identity | May | May not |
|---|---|---|
| Agent (its service account or `pacto:agents`) | propose, read and cancel its own operations | approve, freeze, unfreeze, write status, edit the pointer |
| Person | propose, read and cancel any operation, approve a question their team is asked, freeze, unfreeze | write status |
| Break-glass (`pacto:break-glass`) | override and edit `spec.ref.digest` | |
| Promoter | write status and raise its own operations | sign or push |
| Recorder | write the record fields and finalizer; sign and push records | anything else |

Admission requires an Approval's `spec.by`, `spec.groups` and `spec.authTime`
to match the requesting user and its login time. The promoter does not count
a login older than its login freshness (`delivery.promoter.authFreshness`, 15 minutes
by default), an approval from the operation's own actor or
whoever it acts for, one from outside the teams asked, one given before the
question was last asked, an accepted unknown where an approval is needed or an
approval once the version shipped. The operation's `status.message` says why
an Approval does not count. Once given, an Approval's spec cannot
change; the promoter makes its operation its owner, so deleting the operation
deletes its Approvals. A break-glass edit of the pointer is never undone: it
is recorded as a witnessed override and the Stage freezes.
