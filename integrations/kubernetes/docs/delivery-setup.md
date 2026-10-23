# Delivery reference: setup and gates

Reference for [Pacto delivery](delivery.md). To pactify a service end to end,
follow [Pactify a service](../../pactify.md) instead. The API group is
`delivery.pacto.trianalab.io/v1alpha1` (alpha).

## Install

The chart needs Flux (source and kustomize controllers) and the Pacto operator.
Install the published chart at the same version as your operator:

--8<-- "integrations/kubernetes/docs/generated/_delivery-install-command.md"

The chart has its own image, CRDs, service account, RBAC and admission
policies; the `pacto-operator` chart gains nothing. Its image,
`ghcr.io/trianalab/pacto/delivery`, is signed by the release workflow; see
[Published artifacts](artifact-hub.md).

Bind `pacto-delivery-agent` to your agents' own service accounts and
`pacto-delivery-approver` to the people who may approve.

To run an unreleased change, from the repository root:

```bash
docker build -f integrations/kubernetes/Dockerfile --build-arg CMD=cmd/delivery \
  -t registry.example.com/pacto-delivery:dev .
helm install pacto-delivery integrations/kubernetes/charts/pacto-delivery \
  -n pacto-delivery --create-namespace \
  --set image.repository=registry.example.com/pacto-delivery --set image.tag=dev
```

## Upgrading

Helm installs the delivery CRDs on the first `helm install` and never updates
them, so apply the new ones first. The URLs are pinned to this release:

--8<-- "integrations/kubernetes/docs/generated/_delivery-crd-apply.md"

Then upgrade the release:

--8<-- "integrations/kubernetes/docs/generated/_delivery-upgrade-command.md"

Upgrading the operator never touches these CRDs; see
[Upgrade the Kubernetes operator](upgrade.md).

## How a change moves

A **Stage** is one service in one environment. It names the Flux
`OCIRepository` that applies it, and its `status.pointer` is the digest the
environment runs. Only the promoter moves the pointer, and the only field it
writes on an object it does not own is that repository's `spec.ref.digest`.
Every change is an **Operation** whose spec never changes:

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
  owners. Anything unseen (no contract, an unreadable Pacto, a registry silent
  for 10 seconds) holds it for a person to accept the unknown.
- After: the Pacto contract check is the verify gate. It counts only when
  `status.evaluatedRevision` names this version's contract digest and the
  current workload generation. `Compliant` and `Warning` pass,
  `NonCompliant` and `Invalid` fail, anything else is Unknown.
  [Health checks and claims](delivery-health.md) can join it.
- A failed version is never promoted again on that Stage. The promoter raises
  one rollback per failure; if it fails too or its outcome is unclear, the
  Stage freezes until a person unfreezes it.

A Stage with `auto: true` promotes the newest eligible candidate by itself. A
person's rollback puts an auto-hold on the Stage, so automation does not undo
it.

## Set up a Stage

The candidate `OCIRepository` follows the deploy artifact's repository, and its
`spec.verify` names the publisher, so Flux checks every signature. The Stage
records each digest Flux verified in `status.published` and refuses anything
else as `NotPublished`. Without a `secretRef` or `matchOIDCIdentity` Flux
accepts a keyless signature from anyone, so such a Stage promotes nothing. A
Stage that cannot promote has `Ready` `False` with the reason
`CandidateUnverified`, `CandidateNotFound` or `NoCandidateSource`.

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: payments-candidates, namespace: test}
spec:
  url: oci://registry/apps/payments
  ref: {semver: ">=0.0.0"}
  verify: {provider: cosign, secretRef: {name: cosign-pub}}
  # keyless instead: the identity and issuer of the contract's publish.sign
  # verify:
  #   provider: cosign
  #   matchOIDCIdentity:
  #     - issuer: "^https://token.actions.githubusercontent.com$"
  #       subject: "^https://github.com/acme/payments/.github/workflows/release.yml@refs/heads/main$"
```

Label the `OCIRepository` that applies the Stage, so admission keeps everyone
but the promoter and break-glass off its digest, and give it the same
`spec.verify`. A downstream Stage takes only what its upstream lists in
`status.offered`: versions that passed the published gate and verified there,
never one let through on an accepted unknown.

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
pacto delivery get -n prod <operation>
pacto delivery approve -n prod <operation> --decision approve
```

`plan` predicts each gate's verdict, whether a person must decide and the one
question they would be asked. `propose` with the plan's intent digest refuses
with `PlanStale` if anything moved since. `cancel` works until the pointer
moves; after that, propose `--type rollback --undoes <operation>`. A tag is
refused, and the refusal names its digest.

`pacto mcp --delivery` gives agents `pacto_delivery_operation_types`,
`_describe`, `_plan`, `pacto_delivery_get`, `_explain`, `_history`, `_blame`,
`_undo` and `_record_verify`, plus `_propose` and `pacto_delivery_cancel` only
when the apiserver says the caller is an agent. There is no approve tool. Over
HTTP each request acts as its own caller; see
[sign-in, approvals and agents](delivery-identity.md).

## When another service must move first

When a promote breaks a consumer, the plan names a version of that consumer
to ship first:

```text
move first: orders 1.8.0 runs in prod and accepts payments ^2.0.0, not 3.0.0. orders 1.9.0 (sha256:4f1c...)
accepts it (>=2.0.0 <4.0.0) and was verified on test/orders. Ship orders first: promote sha256:4f1c... on Stage prod/orders.
```

- **Only consumers in that environment count.** A
  `delivery.pacto.trianalab.io/environment` label on a Pacto places it.
  Without one, a Pacto is in an environment when a Stage of it applies into
  the Pacto's namespace (the Kustomization behind its `applyRef` sets
  `targetNamespace`). A Pacto that cannot be placed, or a label naming an
  environment no Stage declares, counts everywhere; when it is a consumer the
  plan names it and asks a person.
- **The candidate accepts both versions**, the provider's running one and the
  new one, so shipping it first breaks nothing. Pacto picks the newest such
  version the consumer's upstream Stage offered.
- **It fits what else runs there**: its other dependencies accept their running
  versions, and every Pacto there with a range on the consumer accepts it.
- **It is newer** than what runs and never ran on that Stage.
- **It says what it cannot see**: a partial graph, an unreadable range or
  contract, no Stage or several for the consumer. All the plan's registry
  reads share one 10 second limit.

It is in `pacto delivery plan` and `explain`, the plan MCP tool, the
Operation's `status.plan` (`order.moveFirst[]` and `order.unknown`) and the
dashboard's **Update these first**. While a change waits, the promoter plans
the order again, so a consumer that moved drops out and its team is no
longer asked: only the teams the change still breaks may approve it, and a
team it newly breaks is asked too. Pacto never promotes the
other service itself, and a person can still approve shipping anyway.

## Who may do what

| Identity | May | May not |
|---|---|---|
| Agent (its own service account or the `pacto:agents` group) | propose, read and cancel operations | approve, unfreeze, write status, edit the pointer |
| Person | the above, plus approve (a question their team is asked) and unfreeze | write status |
| Break-glass (`pacto:break-glass`) | override and edit `spec.ref.digest` | |
| Promoter | write status and raise its own operations | sign or push |
| Recorder | write `status.record`, `status.stream.recordedSeq`, `head` and `anchor`, and the record finalizer; sign and push records | anything else |

An approval is bound to the operation's uid and intent digest. Admission
requires `spec.by` and `spec.groups` to be the requesting user's and
`spec.authTime` to be the login time in `userInfo.extra["pacto.dev/auth-time"]`.
The promoter refuses a login older than 15 minutes, an approval from the
operation's own actor and one from outside the teams the question names. A
break-glass edit of the pointer is never undone: the promoter records it as a
witnessed override, adopts it and freezes the Stage.
