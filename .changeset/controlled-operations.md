---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

A delivery plan or proposal can carry a correlation id, such as a ticket or
incident id: `pacto delivery plan|propose --correlation-id`, `correlation_id`
on `pacto_delivery_operation_plan` and `pacto_delivery_operation_propose`, and
`correlationID` in the dashboard's plan and propose bodies. The id is at most
128 printable ASCII characters, checked by the client and by the operation
admission policy. The automatic rollback, freeze and step-down the promoter
raises after a change inherit its id, so `pacto delivery history
--correlation-id` lists the changes of one incident.

The intent digest now binds the cause a proposal cites (`--cause-verdict`,
`cause_verdict`), never the correlation id, and `PlanStale` names a changed
cause. Proposals that cite a cause need the CLI, the dashboard and the
operator at the same release, in both directions: across releases the client
and the promoter compute different digests, and the promoter finishes the
Operation `Rejected` with `PlanStale`. Proposals without a cause are
unaffected.

An agent must name the person it acts for (`onBehalfOf`, `--on-behalf-of`,
`on_behalf_of`) when it proposes a change, never itself, a service account or
the promoter. The client, the promoter and the operation admission policy
refuse an agent's proposal without one, with the reasons `NoPrincipal`,
`SelfPrincipal` and `NotAPerson`; planning needs none, and changes the
promoter raises itself need none. Existing agent automations that propose
without naming a person must add it. The named person may not approve the
change, and an approval of an agent's change that names nobody is refused.
The name is asserted, never verified: it records who an agent says it acts
for, and it does not stop a person who controls an agent's credentials from
naming someone else and then approving the change.

The dashboard's propose panel proposes the plan it shows when a reason or an
on-behalf-of person is typed, with no false "Something changed" warning. Its delivery
errors carry `differs`, the field that made a plan stale. The propose panel
asks an agent who it acts for and takes a ticket or incident id.
