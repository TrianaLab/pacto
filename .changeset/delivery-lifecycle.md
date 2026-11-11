---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

Follow a service through delivery on every surface. `pacto delivery status`,
the `pacto_delivery_status` MCP tool and the dashboard show each Stage of a
service in promotion order with its upstream, the one state the board counts,
the version, contract and source revision it runs, its newest re-check with
`Watched` and what you may ask for (`offers`: promote, unfreeze, freeze and
undo, each with why or why not). `--why` and `/api/delivery/progress` say why
a candidate cannot move yet, with each reason's fix and a dry-run plan that
proposes nothing. Offers go unavailable when RBAC denies or cannot answer, and
when the delivery identity policy cannot be read, with a `--delivery-namespace`
hint when it is not found. A Stage whose admission policies are missing (Ready
False `Unguarded`) and a Stage that observes (including one that reports no
level) offer no promote, and none appears in `actions`.

`pacto delivery lifecycle`, `pacto_delivery_lifecycle` and
`/api/delivery/lifecycle` follow one version through every Stage, and
`pacto delivery record get`, `pacto_delivery_record_get` and
`/api/delivery/record` read one signed record by its id. History takes
`--cause` and `--correlation-id` (`cause` and `correlation_id` in MCP, which
also accepts durations for `since` and `until`); both narrow the timeline too.
The CLI history shows applied, cause and correlation columns and marks lines
that are not signed. `/api/delivery/plan` and propose return the full plan
answer (`next`, `precedent` and `order`). Published versions show the source
revision `pacto publish` stamps, an annotation, not signed, and link their
fleet revision; a service whose Stages name two domains is listed once per
domain. Delivery service filters match the service name exactly. A Stage
waiting on exactly one held change names it (`heldOperation`). A fleet service detail carries
`placement`, where its targets report it runs. The CLI labels an evidence
gate's Pass as Checked, `pacto.contract` verdicts cite each Pacto's target,
status, evaluation time and findings, a held promote on a frozen Stage names
the `StageFrozen` conflict and the `CandidateUnverified` fix points at
`spec.verify`. The operator uses the shared Ready reasons and auto-promote
rule, and a held shadow promote keeps the person's question.
