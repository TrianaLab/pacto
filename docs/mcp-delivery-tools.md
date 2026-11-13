# MCP publish and delivery tools

This page lists the tools `pacto mcp --publish` and `pacto mcp --delivery`
serve. The [MCP reference](mcp-reference.md) covers the other modes, how they
combine and the tool-call log.

## The publish server

`--publish` adds `pacto_publish_plan` (what publishing would write and which
checks refuse it, without writing) and `pacto_publish` (publishes the version
the same way `pacto publish` does, with this process's registry credentials,
always to a new tag) beside the authoring tools and `pacto_capabilities`,
which reports the `publish` mode with `writes` true. It runs over stdio
only.

## The delivery server

`--delivery` serves the delivery tools over the cluster your kubeconfig
points at: `pacto_delivery_status`, `pacto_delivery_operation_types`, `pacto_delivery_candidates`,
`pacto_delivery_operation_plan`, `pacto_delivery_get`, `pacto_delivery_explain`,
`pacto_delivery_history`, `pacto_delivery_blame`, `pacto_delivery_undo_plan`,
`pacto_delivery_record_get`, `pacto_delivery_record_verify` and
`pacto_delivery_lifecycle` for everyone, plus
`pacto_delivery_operation_propose` and `pacto_delivery_cancel` only when the
cluster says the caller is an agent. Delivery mode has no approve tool. Over
HTTP the server needs
[token exchange](integrations/kubernetes/delivery-identity.md#agents-over-the-network).

## The delivery read tools

Six read tools answer what the dashboard's delivery views show, with the
route's own parameters and answer: `pacto_delivery_caller`,
`pacto_delivery_inbox`, `pacto_delivery_environments`,
`pacto_delivery_autonomy`, `pacto_delivery_published` and
`pacto_delivery_refusals`. They run the dashboard's route code in the server
with the caller's identity, so the API server decides what each caller reads.
With `--fleet` they also follow the caller's fleet view, and the fleet
`--namespace` limits them to one namespace, as on the dashboard. A list answer
carries `asOf` and `completeness`. It covers the namespaces the caller may list
Stages in; namespaces outside the caller's permissions are left out without
being named. `unreadable` names a namespace that refused the list, and without
`--fleet` the inbox names `owners` there, since no owner is known and every
Stage is shown as the caller's.

## Candidates, plans and history

`pacto_delivery_candidates` lists the versions a Stage may take, with their
digests. The `to` input of plan and propose takes a version (`3.0.0`), a tag or
a digest; a version or a tag must name exactly one of those candidates, or the
tool refuses it and says why. A plan's `next` says what proposing it leads
to, as [plan, propose and approve](integrations/kubernetes/delivery-operations.md)
describes, and `pacto_delivery_get` repeats it while an operation is held.
Propose with the plan's intent digest and the same `reason` and `on_behalf_of`: the digest binds them,
and `PlanStale` names the one that differs when it can tell.

`pacto_delivery_history` takes `since` and `until` (an RFC 3339 time or a
duration such as `2h`), `cause`, `correlation_id` and, without a `stage`,
`service` and `domain`.
[Status, progress and lifecycle](integrations/kubernetes/delivery-status.md)
covers the status, lifecycle and record tools.
