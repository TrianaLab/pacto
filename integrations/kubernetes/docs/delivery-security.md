# Delivery reference: security model

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

Every change Pacto delivery makes goes through an Operation. A person, a CI
job and an AI agent over MCP each create one, and the promoter plans and
checks it. The operator chart's admission policies check who writes each
Operation and Approval and who moves a Stage's pointer. This page lists what
those policies close and what your platform closes itself.

## How Pacto tells who is acting

Admission and the promoter read the identity the Kubernetes API server
authenticated. They sort it into four classes, and the first row that matches
wins:

| Class | Who |
|---|---|
| Promoter | The promoter's own ServiceAccount |
| [Break-glass](../../glossary.md#break-glass) | Members of `delivery.identities.breakGlassGroups` |
| Agent | Any other ServiceAccount, any other `system:` user, an unauthenticated request, members of `agentGroups` and, when you set the deprecated `humanGroups`, anyone in none of them |
| Person | Every other signed-in identity |

A CI job that runs under a ServiceAccount counts as an agent. An agent that
holds a person's credentials acts as that person. [Approvals and
agents](delivery-identity.md#who-counts-as-a-person-an-agent-or-a-team) sets
the groups.

## What each admission policy checks

The chart installs these ValidatingAdmissionPolicies whenever delivery is on.
Each one fails closed.

| Policy | What it checks |
|---|---|
| `operation` | An Operation's actor is the user who creates it. Only the promoter raises a witnessed operation (an override it records for a digest it did not write; see [Plan, propose and approve](delivery-operations.md)). Only break-glass raises an override, apart from the promoter's witnessed override. Agents may not freeze, unfreeze, acknowledge or lower a Stage to observe. An agent's Operation names the person it acts for in `spec.onBehalfOf`, never itself, a service account, a system user or the promoter ([agents](delivery-agents.md#an-agent-names-the-person-it-acts-for)). A `spec.correlationID` is at most 128 printable ASCII characters. |
| `cancel` | An agent cancels only an Operation it proposed. Once the pointer has moved, nobody cancels the Operation, and only the promoter or the namespace controller deletes it after it concludes. |
| `approval` | An Approval's author is the user who creates it. Agents and ServiceAccounts may not approve. |
| `pointer` | On a labelled Flux OCIRepository, only the promoter or break-glass changes `spec.ref.digest`. Only break-glass changes its URL, layer selector or ignore rules, or removes the label. |
| `pointer-argocd` | The same rules for `spec.source.targetRevision` on a labelled Argo CD Application. Only break-glass changes its source, destination or project. Installed when `delivery.argocd.enabled` is `true`, or `auto` on a cluster that serves Applications. |
| `applier` | On a labelled Flux Kustomization, only break-glass changes what it applies or removes the label. |
| `applier-helmrelease` | On a labelled Flux HelmRelease, only break-glass changes its chart, chart source, values, post-renderers, target, service account or `suspend`, or removes the label. Installed when the promoter applies through HelmReleases (`delivery.flux.enabled` is `true`, or `auto` on a cluster that serves HelmReleases). |
| `status` | Only the promoter writes the status of a Stage or an Operation, apart from the recorder's record fields and `system:masters`. A person may clear a Stage's `status.upstreamSeen`. |
| `records` | Only the recorder writes record fields, and it writes nothing else. Installed with the recorder. |
| `promoter-writes` | The promoter writes only to labelled pointers, and only their digest. `promoter-writes-argocd` does the same for the target revision of an Application, installed with `pointer-argocd`. |

Only break-glass, `system:masters`, the GitOps controllers in
`delivery.admissionPolicies.gitopsUsers`, the namespace controller and the
garbage collector create or delete a labelled pointer, Kustomization or HelmRelease.

## When a policy is missing

The promoter reads its policies before it acts. If one is missing, fails open,
has a binding that does not deny or binds another policy, or is narrowed by
`matchResources` or `paramRef`, the promoter holds every operation and counts
no approval.
Cluster-admin and `system:masters` can delete the policies. The promoter then
holds every operation until they are back. A promoter started without
`--admission-policies` admits nothing, and every Stage shows `Unguarded`.

## The checks each operation type runs

Every Operation of the same type runs the same checks, whoever raised it. Who
must approve it can depend on who raised it. The types run different checks:

- A promote runs the published, diff, impact and suite checks before it
  applies, and the contract check after.
- Before it applies a rollback, the promoter runs only the impact check, which
  holds it for the consumers' owners when a consumer does not accept the older
  version. The impact check skips the promoter's own rollbacks.
- A break-glass override runs no checks and freezes the Stage until a person
  unfreezes it.

[Plan, propose and approve](delivery-operations.md) lists every type.

## Which publishers Pacto accepts

A first Stage that reads `spec.candidates.registry` lists the newest 20
version tags of its repository and verifies each digest's Sigstore signature
itself, so it needs no Flux
([Argo CD clusters](delivery-reach.md#which-manager-applies-a-stage)). A
digest joins `status.published` only when its signer passes one of three
checks, and each verdict names the one that decided:

| Source | Accepts | Set by |
|---|---|---|
| `rule` | On `ghcr.io/<org>`, a GitHub Actions workflow of `<org>`; on `registry.gitlab.com/<group>`, a GitLab CI pipeline of `<group>`. Both on the default branch, and the repository the certificate attests must match the artifact's `org.opencontainers.image.source` | `delivery.publishers.registry` and `defaultBranch` |
| `pin` | One CI identity (issuer, subject and repository) a person confirmed for one service | the delivery inbox |
| `policy` | The rules of a signed delivery policy, which replace the derived rule once verified | `delivery.publishers.policy` and `policySigners` |

Other registries, such as ECR, Artifact Registry, ACR, Docker Hub, Harbor or
zot, name no CI provider and get no rule: a person confirms the first signer
of each service. The contract never decides: `publish.sign` only says how
`pacto publish` signs.

Unsigned, unreadable or unreachable is `unknown` and never passes, and neither
does a missing trusted root or a policy that does not verify. A person's
signature passes only through a policy rule that names it, never on first
use. A registry-declared revision must reuse its CI-signed baseline's deploy
digest: a deploy digest a person signed is `refused`.

`pacto delivery trust <service>`, the `pacto_delivery_trust` MCP tool and the
dashboard's Published view show the rule, the pins, the signers waiting and
the newest verdicts. [Setup](delivery-setup.md#publishers) lists the values.

### Confirm a new signer

A CI identity no rule, pin or policy covers is a `new-signer`. The promoter
raises one Operation of type `delivery.pacto.trianalab.io/trust` per service
at a time, and the delivery inbox asks "payments: new signer `<subject>`
(issuer, repository, attested). Confirm it may publish payments?".

![The delivery inbox asking whether a new CI signer of audit-log may publish it, with a Confirm identity button](../../assets/screenshots/dashboard-delivery-inbox-new-signer-light.png#only-light)
![The delivery inbox asking whether a new CI signer of audit-log may publish it, with a Confirm identity button](../../assets/screenshots/dashboard-delivery-inbox-new-signer-dark.png#only-dark)

- **Confirm identity** opens the change, where a person approves or rejects
  it; `pacto delivery approve -n <namespace> <operation>` does the same.
  Whoever may update the workload may answer. With team narrowing, only the
  team that owns the running version is asked, never the team the candidate
  names. An agent never decides.
- Once approved, the recorder writes a signed
  [`identity-pin/v1` record](delivery-records.md#identity-pins) and the watch
  accepts that signer. Confirming a signer marks no digest verified: the
  version still passes every gate.
- Other new identities wait in the trust view until the open question
  concludes. A signer that differs from the pinned one asks again ("signer
  changed from ... to ..."). A rejected signer stays out until
  `delivery.recorder.operationTTL` (168h) deletes the operation, then it is
  asked again.
- Pins need the recorder (`delivery.recorder.enabled`) and a key to verify
  them: keyless recorder signing or `delivery.recorder.signing.publicKey`.
  Without them no question is raised, and `status.publisher.message` says
  why.

A pin counts only when it verifies under this cluster's recorder key or a
`delivery.recorder.trust` key that lists an explicit `pins/` stream, and only
when the approval record it cites verifies and answers the trust operation
for the same identity. Deleting the newest pin brings back no older one: the Stage
keeps `status.publisher.pinAt` and judges with no pin while the newest is
gone.

### The signed delivery policy

A security owner who wants explicit rules signs a delivery policy: an in-toto
statement of predicate type `delivery.pacto.trianalab.io/policy/v1` in a
Sigstore bundle with one DSSE signature by a `role: policy` key of
`delivery.publishers.policySigners`, pushed as the one layer of an OCI
artifact. `delivery.publishers.policy` names it by tag.

```yaml
predicate:
  at: "2026-11-17T09:00:00Z"   # signed time; an older policy is refused
  publishers:
    - name: release
      issuer: https://token.actions.githubusercontent.com
      subject: ^https://github\.com/acme/[^/]+/\.github/workflows/release\.yml@refs/heads/main$
      repository: ^https://github\.com/acme/[^/]+$
      ref: refs/heads/main
      services: ["pay*"]        # empty covers every service
  signers:
    - name: release bots
      issuer: https://token.actions.githubusercontent.com
      subject: ^https://github\.com/acme/bots/.+$
      class: agent              # human, agent or ci
```

- Every subject and repository pattern is anchored (`^...$`) and matches no
  identity nobody has, or the policy is refused. An unknown key is refused
  too, so an older promoter keeps its last policy.
- A policy key verifies only policies, never a record or a pin.
- A tag that fails to read or verify, or that moves to a policy signed before
  `status.publisher.policyAt`, keeps the last verified policy. A configured
  policy that never verified accepts only pinned signers.

### Signer classes

Every verified keyless signer carries a class and `classifiedBy`. The first
`signers` rule of the policy that matches decides (`policy:<name>`); otherwise
the default rule (`default:<reason>`) reads a certificate that attests a
source repository, or one from a CI issuer, as `ci`; a SPIFFE, Kubernetes
service account or Google service account subject as `agent`; an email from
an interactive login as `human`; and anything else as `agent`. Only a
`human` signature makes an approval a decision. A CI- or agent-signed
approval record is a proposal and never counts, a trust confirmation
included.

## What stays outside the policies

- The policies match only objects that carry the
  `delivery.pacto.trianalab.io/apply-ref` label. A Stage leaves observe only
  once its pointer carries it.
- Fields outside the rules stay open, such as an OCIRepository's `interval`
  and `verify` or an Argo CD Application's `syncPolicy`.
- The GitOps controllers may create and delete a labelled pointer with no
  digest check. When the promoter finds a digest it did not write, it records
  an override and freezes the Stage.
- No policy matches workloads or the Stage spec. RBAC alone limits
  `kubectl apply` on a Deployment.
- The `-agent` and `-approver` roles may `get`, though not list, a Flux
  HelmRelease or an Argo CD Application they can name, so a plan can find the
  version source. That read includes inline Helm values. Bind these roles
  with a RoleBinding in the Stage namespaces, and keep secrets in
  `valuesFrom` rather than inline values.

Close the rest with RBAC on workloads, Stages and pointer objects, and with
your own admission on workloads.

## Signed records

A change leaves a signed record only when the recorder is on. The recorder is off by default. [Signed records
and history](delivery-records.md) sets it up.
