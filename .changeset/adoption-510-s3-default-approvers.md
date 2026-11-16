---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Delivery tells people from agents by how they sign in and lets whoever may
update a Stage's workload decide its held changes, with no identity values.

Upgrade effect first. An upgrade that takes the new chart's defaults turns
owner-team narrowing and the ownerless protection off unless the install set
`delivery.identities.teamGroupPrefix` explicitly: a values file through Argo
CD, a Flux HelmRelease or `helm upgrade -f`, and the documented
`helm upgrade --reset-then-reuse-values`. To keep the old behaviour, set
`delivery.identities.teamGroupPrefix: ""` and
`delivery.identities.requireOwnerTeam: true`. Plain `--reuse-values` keeps the
old defaults (`""` and `true`) and so keeps narrowing. The promoter emits a
`TeamNarrowingOff` Event on each Operation it holds while narrowing is off, so
the effect shows on the first held change.

- A ServiceAccount, any other `system:` user and an unauthenticated request
  are agents; every other signed-in identity is a person. `agentGroups` marks
  automation that signs in through OIDC. The chart no longer refuses delivery
  with `humanGroups` empty: `allowEmptyHumanGroups` is ignored (the schema
  still accepts it) and `humanGroups` is a deprecated override.
- Approvers need `update` on the Stage's workload, asked as a
  SubjectAccessReview by the promoter (it gains `create subjectaccessreviews`)
  and as a SelfSubjectAccessReview by the dashboard and `pacto delivery
  approve`. The same rule decides a person's unfreeze and acknowledge. Refusals
  read `CannotUpdateWorkload` and `ApproverUnverified`; a failed review never
  allows. With delivery on, the chart aggregates delivery reads into the
  built-in `view`, `edit` and `admin` roles and creating Approvals and
  Operations into `edit` and `admin`, so people with `edit` or `admin` in a
  namespace may decide there without an `-approver` binding.
- `teamGroupPrefix` defaults to `null`: set, even to `""`, it narrows who may
  answer to the owner teams. `requireOwnerTeam` defaults to `false`.
- Operation and Approval records, `pacto delivery blame` and `history`, the
  dashboard and MCP delivery answers carry how each actor was classified
  (`classifiedBy`) and, on each approval, the access phrase, its source and
  when it was answered (`access`, `accessSource`, `accessAt`). Records stay
  `operation/v1`, but signed records are parsed strictly: upgrade a promoter
  that follows another cluster's records (`upstreamTrust`), and the CLI, no
  later than the recording cluster.
- Go API: `delivery.ApprovalRefusal` and `delivery.ApprovalEligible` take a
  last `WorkloadAccess` argument, so no caller can skip the rule.
