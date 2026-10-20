# Pacto delivery

Pacto delivery decides which version each environment runs. It uses your
contracts to work out whether a change is safe, who must agree when it is not
and when to roll back. Flux applies the result.

Delivery is opt-in. Everything else Pacto ships never changes what runs, and
installing it grants no right to. Delivery is the one part that does, so it is
a separate chart (`pacto-delivery`) with its own image, CRDs, service account,
RBAC and admission policies. The `pacto-operator` chart gains nothing.

!!! warning "Alpha"
    The API group is `delivery.pacto.trianalab.io/v1alpha1`. Signed records,
    health checks from Mira and ordering across services come later.

## How it works

A **Stage** is one service in one environment. It names the Flux
`OCIRepository` that applies it, and its `status.pointer` is the artifact
digest the environment runs. Only the promoter moves the pointer, and it only
ever writes one field of an object it does not own: `spec.ref.digest`.

Every change is an **Operation**, a request whose spec never changes after it
is created. The promoter carries it through these steps:

```mermaid
flowchart LR
  P[Pending] -->|gates pass| L[lock]
  P -->|a person must decide| H[Held]
  H -->|approved| L
  L --> R[Running]
  R -->|Flux applied it| V[Verifying]
  V -->|Pass| S[Succeeded]
  V -->|Fail| F[Failed, rolled back]
```

- **Before it moves**, `pacto.diff` compares the running contract with the new
  one and `pacto.impact` projects the change onto every consumer that declares
  a compatibility range. A consumer the new version breaks holds the change and
  routes the question to that consumer's owners. Anything Pacto cannot see (an
  artifact with no contract, a Pacto it cannot read) holds it for a person to
  accept the unknown. Unknown never passes.
- **After Flux applies it**, the Pacto contract check is the verify gate. It
  counts only when the Pacto evaluated exactly this version: the contract
  digest in `status.evaluatedRevision` is the one the artifact names and the
  workload generation it read is current. `Compliant` and `Warning` pass,
  `NonCompliant` and `Invalid` fail, anything else is Unknown.
- **A failed version** is remembered on the Stage and never promoted again. The
  promoter raises one rollback per failure. If that rollback fails too, or the
  outcome cannot be established, the Stage freezes until a person unfreezes it.

A Stage with `auto: true` promotes the newest eligible candidate by itself:
the digest of a candidate `OCIRepository`, or the newest version an upstream
Stage verified. A person's rollback puts an auto-hold on the Stage, so
automation does not undo it.

## Install

The chart needs Flux (source and kustomize controllers) and the Pacto operator.
The image and the chart are not published yet, so build the image from the
repository root and install the chart from the checkout:

```bash
docker build -f integrations/kubernetes/Dockerfile --build-arg CMD=cmd/delivery \
  -t registry.example.com/pacto-delivery:dev .
helm install pacto-delivery integrations/kubernetes/charts/pacto-delivery \
  -n pacto-delivery --create-namespace \
  --set image.repository=registry.example.com/pacto-delivery --set image.tag=dev
```

Then bind `pacto-delivery-agent` to your agents' own service accounts and
`pacto-delivery-approver` to the people who may approve.

## Set up a Stage

Each artifact you deploy is an OCI artifact of manifests whose
`pacto.dev/contract` annotation names the digest-pinned contract it was built
from. With ORAS:

```bash
oras push --annotation "pacto.dev/contract=oci://registry/pactos/payments@sha256:..." \
  registry/apps/payments:candidate manifests.tgz:application/vnd.cncf.flux.content.v1.tar+gzip
```

Label the `OCIRepository` that applies the Stage, so admission keeps everyone
but the promoter and break-glass off its digest, and create the Stage:

```yaml
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata:
  name: payments
  namespace: prod
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

`plan` predicts what the promoter will do: each gate's verdict, whether a
person must decide and the one question they would be asked. `propose` with
the plan's intent digest refuses with `PlanStale` if anything moved since.
`cancel` works until the pointer moves; after that, propose a rollback with
`--type rollback --undoes <operation>`. A tag is refused, and the refusal names
the digest it resolves to.

Agents get the same tools over MCP with `pacto mcp --delivery`:
`pacto_delivery_operation_types`, `_describe`, `_plan` and `pacto_delivery_get`
for anyone, plus `_propose` and `pacto_delivery_cancel` only when the
apiserver says the caller is an agent. There is no approve tool.

## Who may do what

| Identity | May | May not |
|---|---|---|
| Agent (its own service account or the `pacto:agents` group) | propose, read and cancel operations | approve, unfreeze, write status, edit the pointer |
| Person | the above, plus approve and unfreeze | write status |
| Break-glass (`pacto:break-glass`) | override and edit `spec.ref.digest` | |
| Promoter | write status and raise its own operations | sign or push |

An approval is bound to the operation's uid and intent digest. Admission
requires `spec.by` to be the requesting user and `spec.authTime` to be the
login time in `userInfo.extra["pacto.dev/auth-time"]`, which structured
authentication maps from the OIDC `auth_time` claim. The promoter refuses a
login older than 15 minutes and an approval from the operation's own actor.
An agent holding a person's credentials cannot be told apart from that person;
this raises the bar, it does not close the gap.

A break-glass edit of the pointer is never undone. The promoter records it as
a witnessed override, adopts it and freezes the Stage.

## Limitations

- No signed records yet. An operation keeps its record finalizer until it is
  deleted; the promoter then lets it go.
- Stage approval policy is `approval.always`, not an expression.
- Checks do not keep running after a version is verified.
- One cluster, Flux only.
