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
| Agent | Any other ServiceAccount, members of `agentGroups` and, when you set `humanGroups`, anyone in none of them |
| Person | Everyone else |

A CI job that runs under a ServiceAccount counts as an agent. An agent that
holds a person's credentials acts as that person. [Approvals and
agents](delivery-identity.md#who-counts-as-a-person-an-agent-or-a-team) sets
the groups.

## What each admission policy checks

The chart installs these ValidatingAdmissionPolicies when you turn on
delivery, unless a row names another setting. Each one fails closed.

| Policy | What it checks |
|---|---|
| `operation` | An Operation's actor is the user who creates it. Only the promoter raises a witnessed operation (an override it records for a digest it did not write; see [Plan, propose and approve](delivery-operations.md)). Only break-glass raises an override, apart from the promoter's witnessed override. Agents may not freeze, unfreeze, acknowledge or lower a Stage to observe. |
| `cancel` | An agent cancels only an Operation it proposed. Once the pointer has moved, nobody cancels the Operation, and only the promoter or the namespace controller deletes it after it concludes. |
| `approval` | An Approval's author is the user who creates it. Agents and ServiceAccounts may not approve. |
| `pointer` | On a labelled Flux OCIRepository, only the promoter or break-glass changes `spec.ref.digest`. Only break-glass changes its URL, layer selector or ignore rules, or removes the label. |
| `pointer-argocd` | The same rules for `spec.source.targetRevision` on a labelled Argo CD Application. Only break-glass changes its source, destination or project. Installed with `delivery.argocd.enabled`. |
| `applier` | On a labelled Flux Kustomization, only break-glass changes what it applies or removes the label. |
| `status` | Only the promoter writes the status of a Stage or an Operation, apart from the recorder's record fields and `system:masters`. A person may clear a Stage's `status.upstreamSeen`. |
| `records` | Only the recorder writes record fields, and it writes nothing else. Installed with the recorder. |
| `promoter-writes` | The promoter writes only to labelled pointers, and only their digest. `promoter-writes-argocd` does the same for the target revision of an Application. Installed with `delivery.argocd.enabled`. |

Only break-glass, `system:masters`, the GitOps controllers in
`delivery.admissionPolicies.gitopsUsers`, the namespace controller and the
garbage collector create or delete a labelled pointer or Kustomization.

## When a policy is missing

While the chart installs its policies, the promoter reads them before it acts.
If one is missing, fails open or does not deny, the promoter holds every
operation and counts no approval. Cluster-admin and `system:masters` can
delete the policies. The promoter then holds every operation until they are
back.

With `delivery.admissionPolicies.enabled: false` and
`delivery.admissionPolicies.allowUnguarded: true`, the chart installs no
policies, and nothing checks who wrote an Operation or Approval.
The chart refuses `enabled: false` without `allowUnguarded`.

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

Close the rest with RBAC on workloads, Stages and pointer objects, and with
your own admission on workloads.

## Signed records

A change leaves a signed record only when the recorder is on and your registry
serves the OCI Referrers API. The recorder is off by default. [Signed records
and history](delivery-records.md) sets it up.
