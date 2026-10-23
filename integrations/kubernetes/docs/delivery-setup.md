# Delivery reference: setup and gates

Reference for [Pacto delivery](delivery.md). To pactify a service end to end,
follow [Pactify a service](../../pactify.md) instead. The API group is
`delivery.pacto.trianalab.io/v1alpha1` (alpha).

## Install

The chart needs Flux (source and kustomize controllers) and the Pacto operator.
Install it at the same version as your operator:

--8<-- "integrations/kubernetes/docs/generated/_delivery-install-command.md"

The chart brings its own image, CRDs, RBAC and admission policies; the
`pacto-operator` chart gains nothing. Its image,
`ghcr.io/trianalab/pacto/delivery`, is signed by the release workflow; see
[Published artifacts](artifact-hub.md). Bind `pacto-delivery-agent` to your
agents' service accounts and `pacto-delivery-approver` to the people who may
approve.

It needs Kubernetes 1.30 or later. The admission policies are what make the
actor of an Operation and the person behind an Approval true, so the chart
renders them even offline and refuses `admissionPolicies.enabled=false` unless
you also set `admissionPolicies.allowUnguarded=true`. While its operation or
approval policy is missing, the promoter admits no operation and counts no
approval: the operation shows the reason `Unguarded`.

To run an unreleased change, from the repository root:

```bash
docker build -f integrations/kubernetes/Dockerfile --build-arg CMD=cmd/delivery \
  -t registry.example.com/pacto-delivery:dev .
helm install pacto-delivery integrations/kubernetes/charts/pacto-delivery \
  -n pacto-delivery --create-namespace \
  --set image.repository=registry.example.com/pacto-delivery --set image.tag=dev
```

## Upgrading

Helm installs CRDs once and never updates them, so apply the new ones first:

--8<-- "integrations/kubernetes/docs/generated/_delivery-crd-apply.md"

--8<-- "integrations/kubernetes/docs/generated/_delivery-upgrade-command.md"

Upgrading the operator never touches these CRDs; see
[Upgrade the Kubernetes operator](upgrade.md).

## How a change moves

A **Stage** names the Flux `OCIRepository` that applies it, and its
`status.pointer` is the digest the environment runs. Only the promoter moves
it, and the only field it writes on an object it does not own is that
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
  owners; anything unseen (no contract, an unreadable Pacto, a registry silent
  for 10 seconds) holds it for a person to accept the unknown.
- After: the Pacto contract check counts only when
  `status.evaluatedRevision` names this version's contract digest and the
  current workload generation. `Compliant` and `Warning` pass, `NonCompliant`
  and `Invalid` fail, anything else is Unknown.
  [Health checks](delivery-health.md) can join it.
- A failed version is never promoted again on that Stage. The promoter raises
  one rollback per failure; if that fails too or is unclear, the Stage freezes
  until a person unfreezes it. A person's rollback puts an auto-hold on an
  `auto: true` Stage, so automation does not undo it.

Only the four built-in operation types exist: promote, rollback, override and
unfreeze. Stage approval policy is `approval.always`, not an expression.

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

Label the `OCIRepository` that applies the Stage, so admission keeps everyone
but the promoter and break-glass off its digest, and give it the same
`spec.verify`. A downstream Stage takes only what its upstream lists in
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
  auto: true
  gates:
    verify: {window: 5m}
```

## Plan, propose and approve

```bash
pacto delivery plan -n prod --stage payments --to sha256:...
pacto delivery propose -n prod --stage payments --to sha256:... --intent-digest sha256:...
pacto delivery approve -n prod <operation> --decision approve
```

`plan` predicts each gate's verdict and the one question a person would be
asked. `propose` with the plan's intent digest refuses with `PlanStale` if
anything moved since. `cancel` works until the pointer moves; after that,
propose `--type rollback --undoes <operation>`. A tag is refused.

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
| Agent (its service account or `pacto:agents`) | propose, read and cancel | approve, unfreeze, write status, edit the pointer |
| Person | the above, approve a question their team is asked, unfreeze | write status |
| Break-glass (`pacto:break-glass`) | override and edit `spec.ref.digest` | |
| Promoter | write status and raise its own operations | sign or push |
| Recorder | write the record fields and finalizer; sign and push records | anything else |

Admission requires an Approval's `spec.by`, `spec.groups` and `spec.authTime`
to match the requesting user and its login time. The promoter refuses a login
older than 15 minutes, an approval from the operation's own actor and one from
outside the teams asked. A break-glass edit of the pointer is never undone: it
is recorded as a witnessed override and the Stage freezes.
