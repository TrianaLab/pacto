<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from config/crd/bases/*.yaml.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# CRD reference

Custom Resource Definitions installed by the operator. Field lists below are generated from the authoritative OpenAPI v3 schema in `config/crd/bases/`.


## Approval

- **API group**: `delivery.pacto.trianalab.io`
- **Version**: `v1alpha1`
- **Kind**: `Approval`
- **Plural**: `approvals`
- **Scope**: Namespaced

### Additional printer columns

| Name | Type | JSONPath |
| --- | --- | --- |
| Operation | string | `.spec.operation` |
| Decision | string | `.spec.decision` |
| By | string | `.spec.by` |
| Age | date | `.metadata.creationTimestamp` |

### Fields

| Field | Type | Required | Default | Enum | Description |
| --- | --- | --- | --- | --- | --- |
| `spec` | `object` | yes |  |  | ApprovalSpec is a human decision bound to one operation uid and intent digest. |
| `spec.authTime` | `string` | no |  |  | AuthTime is the login time the apiserver reported for that user (userInfo.extra["pacto.dev/auth-time"], RFC 3339 or Unix seconds). |
| `spec.by` | `string` | yes |  |  | By must equal the apiserver username of whoever created the Approval. |
| `spec.decision` | `string` | yes |  |  | Decision is what a human approval says. |
| `spec.groups` | `[]string` | no |  |  | Groups must equal the groups of whoever created the Approval. The promoter accepts it only when one of them names a team the question was routed to. |
| `spec.intentDigest` | `string` | yes |  |  |  |
| `spec.note` | `string` | no |  |  |  |
| `spec.operation` | `string` | yes |  |  | Operation is the operation's name, for people. Matching uses the uid. |
| `spec.operationUID` | `string` | yes |  |  |  |


## Operation

- **API group**: `delivery.pacto.trianalab.io`
- **Version**: `v1alpha1`
- **Kind**: `Operation`
- **Plural**: `operations`
- **Scope**: Namespaced

### Additional printer columns

| Name | Type | JSONPath |
| --- | --- | --- |
| Type | string | `.spec.type` |
| Stage | string | `.spec.stage` |
| Phase | string | `.status.phase` |
| Outcome | string | `.status.outcome` |
| Reason | string | `.status.reason` |
| Age | date | `.metadata.creationTimestamp` |

### Fields

| Field | Type | Required | Default | Enum | Description |
| --- | --- | --- | --- | --- | --- |
| `spec` | `object` | yes |  |  | OperationSpec is the intent. It is frozen at admission. |
| `spec.actor` | `object` | yes |  |  | Actor is the apiserver identity that created the operation. |
| `spec.actor.groups` | `[]string` | no |  |  |  |
| `spec.actor.user` | `string` | yes |  |  |  |
| `spec.cause` | `object` | no |  |  | Cause is why an operation was raised. A person or an agent sets one field; a verdict they cite is re-run when the operation is admitted. An automatic rollback names the failed operation and, when a gate failed it, the verdict. |
| `spec.cause.operation` | `string` | no |  |  | Operation is the uid of the operation that caused this one. |
| `spec.cause.reason` | `string` | no |  |  |  |
| `spec.cause.verdict` | `object` | no |  |  | Verdict is one gate's answer plus a citation that can be re-run. |
| `spec.cause.verdict.citation` | `object` | no |  |  |  |
| `spec.cause.verdict.gate` | `string` | yes |  |  |  |
| `spec.cause.verdict.message` | `string` | no |  |  |  |
| `spec.cause.verdict.phase` | `string` | yes |  | `pre`, `verify` |  |
| `spec.cause.verdict.result` | `string` | yes |  | `Pass`, `Fail`, `Unknown` | Result is a gate verdict. |
| `spec.correlationID` | `string` | no |  |  |  |
| `spec.facet` | `string` | yes |  | `pointer`, `freeze` |  |
| `spec.intentDigest` | `string` | no |  |  | IntentDigest is what plan() returned; the promoter recomputes it. |
| `spec.onBehalfOf` | `object` | no |  |  | Principal is a person an agent says it acts for. It is asserted, never verified. |
| `spec.onBehalfOf.user` | `string` | yes |  |  |  |
| `spec.params` | `object` | no |  |  | Params are the Tier 0 operation parameters. Digests are OCI manifest digests. |
| `spec.params.from` | `string` | no |  |  |  |
| `spec.params.to` | `string` | no |  |  |  |
| `spec.params.undoes` | `string` | no |  |  | Undoes is the uid of the operation a rollback undoes. |
| `spec.reason` | `string` | no |  |  |  |
| `spec.stage` | `string` | yes |  |  | Stage is the name of a Stage in the operation's namespace. |
| `spec.type` | `string` | yes |  | `pacto.dev/promote`, `pacto.dev/rollback`, `pacto.dev/override`, `pacto.dev/unfreeze` |  |
| `spec.witnessed` | `boolean` | no |  |  | Witnessed marks an override the promoter raised for an edit it saw but did not make. Only the promoter may set it. |
| `status` | `object` | no |  |  | OperationStatus is written only by the promoter. |
| `status.actorClass` | `string` | no |  |  | ActorClass is who raised an operation, as the promoter classifies the apiserver identity. It is derived, never asserted by the client. |
| `status.applied` | `boolean` | no |  |  | Applied is true once the pointer moved for this operation. |
| `status.approvals` | `[]object` | no |  |  |  |
| `status.approvals[].authTime` | `string` | no |  |  |  |
| `status.approvals[].by` | `string` | yes |  |  |  |
| `status.approvals[].decision` | `string` | yes |  |  | Decision is what a human approval says. |
| `status.approvals[].name` | `string` | yes |  |  |  |
| `status.approvals[].note` | `string` | no |  |  |  |
| `status.message` | `string` | no |  |  |  |
| `status.outcome` | `string` | no |  |  | Outcome is how a concluded operation ended. |
| `status.phase` | `string` | no |  |  | Phase is where an operation is in its lifecycle. |
| `status.plan` | `object` | no |  |  | Plan is plan()'s answer. It binds to the intent digest. |
| `status.plan.conflicts` | `[]string` | no |  |  |  |
| `status.plan.floor` | `object` | no |  |  | Floor is the approval floor plan() computed: whether a human must decide, which decision is asked for and whose. |
| `status.plan.floor.consumers` | `[]string` | no |  |  | Consumers are the services that made the floor. |
| `status.plan.floor.decision` | `string` | no |  |  | Decision is what a human approval says. |
| `status.plan.floor.human` | `boolean` | no |  |  |  |
| `status.plan.floor.owners` | `[]string` | no |  |  | Owners are the teams the question is routed to and the only ones whose members may answer it: the owners of the consumers that made the floor, or else the team of the Stage's own service. Empty means no team is declared, and any person may answer. |
| `status.plan.from` | `string` | no |  |  |  |
| `status.plan.impactDigest` | `string` | no |  |  | ImpactDigest is the sha256 of the pkg/impact answer the floor came from. |
| `status.plan.intentDigest` | `string` | yes |  |  |  |
| `status.plan.order` | `object` | no |  |  | Order says what must move first when a consumer running in this environment does not accept the new version. Pacto only says it: it never moves another service itself. |
| `status.plan.order.moveFirst` | `[]object` | yes |  |  | MoveFirst names every consumer running in this environment whose declared range does not accept the new version. |
| `status.plan.order.moveFirst[].accepts` | `string` | no |  |  | Accepts is the range that version declares on the provider. |
| `status.plan.order.moveFirst[].candidate` | `object` | no |  |  | Candidate is the newest version of the consumer its upstream Stage offered and verified that is newer than the one running here, accepts both the provider version running here and the new one, accepts what its other dependencies run here and is accepted by the range every Pacto here declares on it, so it can move first without breaking a range Pacto can see. Absent when there is none or Pacto cannot tell. |
| `status.plan.order.moveFirst[].candidate.accepts` | `string` | no |  |  | Accepts is the range it declares on the provider; empty when it no longer depends on it. |
| `status.plan.order.moveFirst[].candidate.digest` | `string` | yes |  |  |  |
| `status.plan.order.moveFirst[].candidate.verifiedOn` | `object` | yes |  |  | VerifiedOn is the upstream Stage that verified it. |
| `status.plan.order.moveFirst[].candidate.verifiedOn.name` | `string` | yes |  |  |  |
| `status.plan.order.moveFirst[].candidate.verifiedOn.namespace` | `string` | no |  |  |  |
| `status.plan.order.moveFirst[].candidate.version` | `string` | no |  |  |  |
| `status.plan.order.moveFirst[].domain` | `string` | no |  |  |  |
| `status.plan.order.moveFirst[].message` | `string` | yes |  |  | Message says all of the above in plain words. |
| `status.plan.order.moveFirst[].service` | `string` | yes |  |  | Service is the consumer. |
| `status.plan.order.moveFirst[].stage` | `object` | no |  |  | Stage is the consumer's Stage in this environment, the one to promote. |
| `status.plan.order.moveFirst[].stage.name` | `string` | yes |  |  |  |
| `status.plan.order.moveFirst[].stage.namespace` | `string` | no |  |  |  |
| `status.plan.order.moveFirst[].unknown` | `string` | no |  |  | Unknown says why Pacto cannot tell whether such a version exists. |
| `status.plan.order.moveFirst[].version` | `string` | no |  |  | Version is the consumer version running here that does not accept the new provider version. |
| `status.plan.order.unknown` | `[]string` | no |  |  | Unknown says why the list may be incomplete: a partial graph, a range Pacto cannot read or a consumer it cannot place. Empty means Pacto saw every consumer running here. |
| `status.plan.precedent` | `object` | no |  |  | Precedent is what happened the last times this version was the target of a recorded operation, on any Stage. Clients fill it from records; the promoter leaves it empty. |
| `status.plan.precedent.completeness` | `string` | yes |  |  |  |
| `status.plan.precedent.entries` | `[]object` | no |  |  |  |
| `status.plan.precedent.entries[].finished` | `string` | no |  |  |  |
| `status.plan.precedent.entries[].outcome` | `string` | yes |  |  | Outcome is how a concluded operation ended. |
| `status.plan.precedent.entries[].reason` | `string` | no |  |  |  |
| `status.plan.precedent.entries[].record` | `string` | yes |  |  |  |
| `status.plan.precedent.entries[].seq` | `integer` | yes |  |  |  |
| `status.plan.precedent.entries[].stream` | `string` | yes |  |  |  |
| `status.plan.precedent.entries[].type` | `string` | yes |  |  |  |
| `status.plan.precedent.entries[].verification` | `string` | no |  |  | Verification qualifies a Succeeded outcome. |
| `status.plan.precedent.note` | `string` | no |  |  |  |
| `status.plan.predicted` | `string` | yes |  |  | Prediction is what plan() expects the promoter to do. |
| `status.plan.question` | `string` | no |  |  | Question is the one question a human would be asked. |
| `status.plan.to` | `string` | no |  |  |  |
| `status.plan.type` | `string` | yes |  |  |  |
| `status.plan.verdicts` | `[]object` | no |  |  |  |
| `status.plan.verdicts[].citation` | `object` | no |  |  |  |
| `status.plan.verdicts[].gate` | `string` | yes |  |  |  |
| `status.plan.verdicts[].message` | `string` | no |  |  |  |
| `status.plan.verdicts[].phase` | `string` | yes |  | `pre`, `verify` |  |
| `status.plan.verdicts[].result` | `string` | yes |  | `Pass`, `Fail`, `Unknown` | Result is a gate verdict. |
| `status.pointer` | `object` | no |  |  | PointerChange is the pointer before and after an operation. |
| `status.pointer.after` | `string` | no |  |  |  |
| `status.pointer.before` | `string` | no |  |  |  |
| `status.policy` | `object` | no |  |  | Policy is the Stage spec the operation concluded under, taken when it concluded, so its record says what governed it however late it is written. |
| `status.policy.applyRef` | `object` | yes |  |  | ApplyRef is the Flux OCIRepository the promoter owns. It only ever sets spec.ref.digest on it. |
| `status.policy.applyRef.name` | `string` | yes |  |  |  |
| `status.policy.approval` | `object` | no |  |  | ApprovalPolicy can only raise the approval floor, never lower it. |
| `status.policy.approval.always` | `boolean` | no |  |  | Always holds every promote for a human, whatever the contracts say. |
| `status.policy.auto` | `boolean` | no |  |  | Auto promotes the newest eligible candidate without anyone asking. |
| `status.policy.candidates` | `object` | no |  |  | Candidates says where new versions come from. Exactly one is set. |
| `status.policy.candidates.ociRepositoryRef` | `object` | no |  |  | OCIRepositoryRef is a Flux OCIRepository the promoter only reads: the candidate is the @sha256: part of its status.artifact.revision. |
| `status.policy.candidates.ociRepositoryRef.name` | `string` | yes |  |  |  |
| `status.policy.candidates.upstreamStage` | `object` | no |  |  | UpstreamStage offers that Stage's verified digests minus its failed ones. |
| `status.policy.candidates.upstreamStage.name` | `string` | yes |  |  |  |
| `status.policy.candidates.upstreamStage.namespace` | `string` | no |  |  |  |
| `status.policy.deadlines` | `object` | no |  |  | Deadlines override the operation type's defaults (Go durations). |
| `status.policy.deadlines.approval` | `string` | no |  |  |  |
| `status.policy.deadlines.execution` | `string` | no |  |  |  |
| `status.policy.environment` | `string` | yes |  |  |  |
| `status.policy.gates` | `object` | no |  |  | Gates configures the Stage's gates. The pre-gates are fixed by the operation type; the Pacto contract check always verifies, beside the Stage's checks. |
| `status.policy.gates.verify` | `object` | no |  |  | VerifyGates configures what runs after a version is applied. |
| `status.policy.gates.verify.checks` | `[]object` | no |  |  | Checks are this Stage's own verify gates, run beside the Pacto contract check: a health provider the delivery chart configures, or the built-in pacto.runtime claim gate. |
| `status.policy.gates.verify.checks[].name` | `string` | yes |  |  | Name is the gate id its verdicts carry, unique on the Stage. |
| `status.policy.gates.verify.checks[].params` | `object` | no |  |  | Params are the provider's settings (thresholds, filters). |
| `status.policy.gates.verify.checks[].provider` | `string` | yes |  |  | Provider is a gate provider configured in the delivery chart, or pacto.runtime. A Stage cannot name a URL: providers are operator configuration. |
| `status.policy.gates.verify.recheck` | `string` | no |  |  | Recheck is how often the verify gates run again on the version the Stage runs once it shipped (a Go duration, default 1m). A confirmed Fail rolls it back; Unknown never does. |
| `status.policy.gates.verify.window` | `string` | no |  |  | Window is how long the verify gates may stay Unknown before the operation is held for a human (a Go duration, default 5m). Re-checks after release judge the same length of time. |
| `status.policy.service` | `object` | yes |  |  | ServiceRef is the Pacto ServiceID a Stage delivers: the domain-qualified service name its contracts declare. |
| `status.policy.service.domain` | `string` | no |  |  | Domain is the registry and organisation scope (empty for the default). |
| `status.policy.service.name` | `string` | yes |  |  | Name is the contract's service.name. |
| `status.question` | `string` | no |  |  | Question is what a human is asked while the operation is Held. |
| `status.reason` | `string` | no |  |  | Reason is the machine reason for the outcome or the hold (PlanStale, PointerMoved, NoStage, ...). |
| `status.record` | `object` | no |  |  | Record is the signed record of this operation, once the recorder wrote it. Only the recorder writes it. |
| `status.record.digest` | `string` | yes |  |  | Digest is the record id: the sha256 of its signed payload. |
| `status.record.kind` | `string` | yes |  |  | Kind is operation, or unrecordable when a pre-sign check refused it. |
| `status.record.refused` | `string` | no |  |  | Refused names the check and why, for an unrecordable record. |
| `status.record.seq` | `integer` | yes |  |  | Seq is the record's place in the stream. |
| `status.seq` | `integer` | no |  |  |  |
| `status.times` | `object` | no |  |  | Times are RFC 3339 timestamps. |
| `status.times.admitted` | `string` | no |  |  |  |
| `status.times.applied` | `string` | no |  |  |  |
| `status.times.approvalDeadline` | `string` | no |  |  |  |
| `status.times.executionDeadline` | `string` | no |  |  |  |
| `status.times.finished` | `string` | no |  |  |  |
| `status.times.started` | `string` | no |  |  |  |
| `status.times.verifyDeadline` | `string` | no |  |  |  |
| `status.verdicts` | `[]object` | no |  |  |  |
| `status.verdicts[].citation` | `object` | no |  |  |  |
| `status.verdicts[].gate` | `string` | yes |  |  |  |
| `status.verdicts[].message` | `string` | no |  |  |  |
| `status.verdicts[].phase` | `string` | yes |  | `pre`, `verify` |  |
| `status.verdicts[].result` | `string` | yes |  | `Pass`, `Fail`, `Unknown` | Result is a gate verdict. |
| `status.verification` | `string` | no |  |  | Verification qualifies a Succeeded outcome. |


## Stage

- **API group**: `delivery.pacto.trianalab.io`
- **Version**: `v1alpha1`
- **Kind**: `Stage`
- **Plural**: `stages`
- **Scope**: Namespaced

### Additional printer columns

| Name | Type | JSONPath |
| --- | --- | --- |
| Service | string | `.spec.service.name` |
| Env | string | `.spec.environment` |
| Ready | string | `.status.conditions[?(@.type=="Ready")].status` |
| Pointer | string | `.status.pointer` |
| Frozen | boolean | `.status.frozen` |
| Hold | boolean | `.status.autoHold` |
| Age | date | `.metadata.creationTimestamp` |

### Fields

| Field | Type | Required | Default | Enum | Description |
| --- | --- | --- | --- | --- | --- |
| `spec` | `object` | yes |  |  | StageSpec is one service in one environment. |
| `spec.applyRef` | `object` | yes |  |  | ApplyRef is the Flux OCIRepository the promoter owns. It only ever sets spec.ref.digest on it. |
| `spec.applyRef.name` | `string` | yes |  |  |  |
| `spec.approval` | `object` | no |  |  | ApprovalPolicy can only raise the approval floor, never lower it. |
| `spec.approval.always` | `boolean` | no |  |  | Always holds every promote for a human, whatever the contracts say. |
| `spec.auto` | `boolean` | no |  |  | Auto promotes the newest eligible candidate without anyone asking. |
| `spec.candidates` | `object` | no |  |  | Candidates says where new versions come from. Exactly one is set. |
| `spec.candidates.ociRepositoryRef` | `object` | no |  |  | OCIRepositoryRef is a Flux OCIRepository the promoter only reads: the candidate is the @sha256: part of its status.artifact.revision. |
| `spec.candidates.ociRepositoryRef.name` | `string` | yes |  |  |  |
| `spec.candidates.upstreamStage` | `object` | no |  |  | UpstreamStage offers that Stage's verified digests minus its failed ones. |
| `spec.candidates.upstreamStage.name` | `string` | yes |  |  |  |
| `spec.candidates.upstreamStage.namespace` | `string` | no |  |  |  |
| `spec.deadlines` | `object` | no |  |  | Deadlines override the operation type's defaults (Go durations). |
| `spec.deadlines.approval` | `string` | no |  |  |  |
| `spec.deadlines.execution` | `string` | no |  |  |  |
| `spec.environment` | `string` | yes |  |  |  |
| `spec.gates` | `object` | no |  |  | Gates configures the Stage's gates. The pre-gates are fixed by the operation type; the Pacto contract check always verifies, beside the Stage's checks. |
| `spec.gates.verify` | `object` | no |  |  | VerifyGates configures what runs after a version is applied. |
| `spec.gates.verify.checks` | `[]object` | no |  |  | Checks are this Stage's own verify gates, run beside the Pacto contract check: a health provider the delivery chart configures, or the built-in pacto.runtime claim gate. |
| `spec.gates.verify.checks[].name` | `string` | yes |  |  | Name is the gate id its verdicts carry, unique on the Stage. |
| `spec.gates.verify.checks[].params` | `object` | no |  |  | Params are the provider's settings (thresholds, filters). |
| `spec.gates.verify.checks[].provider` | `string` | yes |  |  | Provider is a gate provider configured in the delivery chart, or pacto.runtime. A Stage cannot name a URL: providers are operator configuration. |
| `spec.gates.verify.recheck` | `string` | no |  |  | Recheck is how often the verify gates run again on the version the Stage runs once it shipped (a Go duration, default 1m). A confirmed Fail rolls it back; Unknown never does. |
| `spec.gates.verify.window` | `string` | no |  |  | Window is how long the verify gates may stay Unknown before the operation is held for a human (a Go duration, default 5m). Re-checks after release judge the same length of time. |
| `spec.service` | `object` | yes |  |  | ServiceRef is the Pacto ServiceID a Stage delivers: the domain-qualified service name its contracts declare. |
| `spec.service.domain` | `string` | no |  |  | Domain is the registry and organisation scope (empty for the default). |
| `spec.service.name` | `string` | yes |  |  | Name is the contract's service.name. |
| `status` | `object` | no |  |  | StageStatus is written only by the promoter's operation reconciler. |
| `status.autoHold` | `boolean` | no |  |  | AutoHold stops auto-promotion after a person chose an older version. |
| `status.autoRollbacks` | `object` | no |  |  | AutoRollbacks counts the automatic rollbacks raised per cause. |
| `status.candidate` | `string` | no |  |  | Candidate is the newest eligible candidate the stage reconciler saw. |
| `status.conditions` | `[]object` | no |  |  | Conditions holds Ready. It is False, naming what is missing, when the Stage can never promote: its candidate OCIRepository does not exist or sets no spec.verify naming the publisher, or it names no candidate source at all. The recorder adds RecordStoreReady: False when the registry has no Referrers API, cannot be reached or holds a stream it refuses to fork. |
| `status.conditions[].message` | `string` | no |  |  |  |
| `status.conditions[].reason` | `string` | no |  |  |  |
| `status.conditions[].status` | `string` | yes |  | `True`, `False`, `Unknown` |  |
| `status.conditions[].type` | `string` | yes |  |  |  |
| `status.failedDigests` | `[]string` | no |  |  | FailedDigests are never promoted again (cap 50). |
| `status.frozen` | `boolean` | no |  |  |  |
| `status.frozenReason` | `string` | no |  |  |  |
| `status.lastCheck` | `object` | no |  |  | LastCheck is the newest re-check of the running version after it shipped. |
| `status.lastCheck.at` | `string` | yes |  |  | At is when the gates ran (RFC 3339). |
| `status.lastCheck.digest` | `string` | yes |  |  |  |
| `status.lastCheck.operation` | `string` | yes |  |  | Operation is the uid of the operation that shipped the version. |
| `status.lastCheck.verdicts` | `[]object` | no |  |  |  |
| `status.lastCheck.verdicts[].citation` | `object` | no |  |  |  |
| `status.lastCheck.verdicts[].gate` | `string` | yes |  |  |  |
| `status.lastCheck.verdicts[].message` | `string` | no |  |  |  |
| `status.lastCheck.verdicts[].phase` | `string` | yes |  | `pre`, `verify` |  |
| `status.lastCheck.verdicts[].result` | `string` | yes |  | `Pass`, `Fail`, `Unknown` | Result is a gate verdict. |
| `status.lastOp` | `string` | no |  |  | LastOp is the uid of the operation that last moved the pointer. |
| `status.locks` | `object` | no |  |  | Locks maps a facet to the uid of the operation holding it. |
| `status.offered` | `[]string` | no |  |  | Offered holds the digests a promote carried through pacto.published with a pass (not an accepted unknown) and that then verified here, oldest first (cap 50). Only these are offered to a downstream Stage. |
| `status.pointer` | `string` | no |  |  | Pointer is the digest the environment runs. |
| `status.published` | `[]string` | no |  |  | Published holds the candidate digests Flux verified the signature of on the candidate OCIRepository (spec.verify naming the publisher's identity), oldest first (cap 50). Only these are promoted from the OCIRepository. |
| `status.stream` | `object` | no |  |  | Stream numbers a Stage's concluded operations. |
| `status.stream.anchor` | `string` | no |  |  | Anchor is where the stream's records are listed: &lt;repository&gt;@<anchor digest>. Only the recorder writes it. |
| `status.stream.concluded` | `[]object` | no |  |  | Concluded are the newest operations given a seq, each with how it concluded, written in the compare-and-swap that took the seq. An operation whose own status write then failed concludes again from here: the same seq and outcome, never a second seq. |
| `status.stream.concluded[].outcome` | `string` | no |  |  | Outcome is how a concluded operation ended. |
| `status.stream.concluded[].reason` | `string` | no |  |  | Reason is "Code: message". |
| `status.stream.concluded[].seq` | `integer` | yes |  |  |  |
| `status.stream.concluded[].uid` | `string` | yes |  |  |  |
| `status.stream.concluded[].verification` | `string` | no |  |  | Verification qualifies a Succeeded outcome. |
| `status.stream.head` | `string` | no |  |  | Head is the digest of the newest record in the stream. Only the recorder writes it. |
| `status.stream.id` | `string` | no |  |  | ID is &lt;clusterID&gt;/&lt;namespace&gt;/&lt;name&gt;; the cluster id is the kube-system namespace uid. |
| `status.stream.recordedSeq` | `integer` | no |  |  | RecordedSeq is the newest local seq the recorder wrote or adopted a record for. Only the recorder writes it. |
| `status.stream.seq` | `integer` | no |  |  |  |
| `status.verified` | `[]string` | no |  |  | Verified holds the newest verified digests, oldest first (cap 10). |


## NodeRuntimeObservation

- **API group**: `pacto.trianalab.io`
- **Version**: `v1alpha1`
- **Kind**: `NodeRuntimeObservation`
- **Plural**: `noderuntimeobservations`
- **Scope**: Namespaced

### Additional printer columns

| Name | Type | JSONPath |
| --- | --- | --- |
| Node | string | `.status.nodeName` |
| State | string | `.status.collection.state` |
| Last Published | date | `.status.lastPublished` |

### Fields

| Field | Type | Required | Default | Enum | Description |
| --- | --- | --- | --- | --- | --- |
| `status` | `object` | no |  |  | status is the agent's published observation. |
| `status.agentVersion` | `string` | no |  |  | AgentVersion is the node agent build version. |
| `status.collection` | `object` | no |  |  | Collection is the health of the kernel collection. |
| `status.collection.droppedEvents` | `integer` | no |  |  | DroppedEvents is the cumulative number of kernel events lost because the ring buffer was full, since the agent started. |
| `status.collection.lastDropAt` | `string` | no |  |  | LastDropAt is when the agent last saw DroppedEvents increase. |
| `status.collection.lastResyncAt` | `string` | no |  |  | LastResyncAt is when the agent last completed a /proc baseline scan. |
| `status.collection.message` | `string` | no |  |  | Message is a human-readable explanation of a non-Running state. |
| `status.collection.reason` | `string` | no |  |  | Reason is a CamelCase machine-readable cause for a non-Running state. |
| `status.collection.state` | `string` | yes |  | `Running`, `Degraded`, `Failed` | State is Running, Degraded or Failed. |
| `status.kernelRelease` | `string` | no |  |  | KernelRelease is the node's kernel release (uname -r). |
| `status.lastPublished` | `string` | no |  |  | LastPublished is the heartbeat: the time of the most recent publish. |
| `status.nodeName` | `string` | no |  |  | NodeName is the Kubernetes node this object describes. |
| `status.observingSince` | `string` | no |  |  | ObservingSince is when this agent process started. |
| `status.pods` | `[]object` | no |  |  | Pods are the pods running on the node, sorted by UID. |
| `status.pods[].baselineAt` | `string` | no |  |  | BaselineAt is the last /proc scan that saw the pod. |
| `status.pods[].connections` | `[]object` | no |  |  | Connections are the pod's outbound TCP peers within the retention window. |
| `status.pods[].connections[].address` | `string` | yes |  |  | Address is the peer IP address. |
| `status.pods[].connections[].failures` | `integer` | no |  |  | Failures counts handshakes that went from SYN_SENT straight to CLOSE (refused, reset or timed out). |
| `status.pods[].connections[].lastFailureAt` | `string` | no |  |  | LastFailureAt is the time of the most recent failure. |
| `status.pods[].connections[].lastSuccessAt` | `string` | no |  |  | LastSuccessAt is the time of the most recent success. |
| `status.pods[].connections[].port` | `integer` | yes |  |  | Port is the peer port. |
| `status.pods[].connections[].successes` | `integer` | no |  |  | Successes counts handshakes that reached ESTABLISHED. |
| `status.pods[].listeners` | `[]object` | no |  |  | Listeners are the pod's TCP listening sockets. |
| `status.pods[].listeners[].address` | `string` | no |  |  | Address is the bound local address (0.0.0.0 or :: for all addresses). A loopback-only listener does not serve traffic sent to the pod IP. |
| `status.pods[].listeners[].family` | `string` | yes |  | `IPv4`, `IPv6` | Family is the socket address family. |
| `status.pods[].listeners[].port` | `integer` | yes |  |  | Port is the local (container) port. |
| `status.pods[].listeners[].process` | `string` | no |  |  | Process is the command name of the process that opened the listener, when the agent saw it happen (empty when only the /proc baseline knows it). |
| `status.pods[].netnsShared` | `boolean` | no |  |  | NetnsShared is true when the pod's network namespace is not its own (the host namespace for hostNetwork pods). Socket events in it cannot be attributed to the pod, so listeners and connections stay empty. |
| `status.pods[].observedSince` | `string` | no |  |  | ObservedSince is when the agent first mapped the pod's network namespace. |
| `status.pods[].truncated` | `boolean` | no |  |  | Truncated is true when a per-pod or per-node bound was hit, so listeners or connections may be missing entries. |
| `status.pods[].uid` | `string` | yes |  |  | UID is the pod UID, read from the pod's cgroup path. |
| `status.publishIntervalSeconds` | `integer` | no |  |  | PublishIntervalSeconds is how often the agent publishes; readers derive staleness from it. |


## PactoRevision

- **API group**: `pacto.trianalab.io`
- **Version**: `v1alpha1`
- **Kind**: `PactoRevision`
- **Plural**: `pactorevisions`
- **Scope**: Namespaced

### Additional printer columns

| Name | Type | JSONPath |
| --- | --- | --- |
| Version | string | `.spec.version` |
| Pacto | string | `.spec.pactoRef` |
| Resolved | boolean | `.status.resolved` |
| Age | date | `.metadata.creationTimestamp` |

### Fields

| Field | Type | Required | Default | Enum | Description |
| --- | --- | --- | --- | --- | --- |
| `spec` | `object` | yes |  |  | spec defines the desired state of PactoRevision. |
| `spec.pactoRef` | `string` | yes |  |  | PactoRef is the name of the parent Pacto resource that owns this revision. |
| `spec.serviceName` | `string` | no |  |  | ServiceName is the service name from the contract. |
| `spec.source` | `object` | yes |  |  | Source specifies where this revision's contract was loaded from. |
| `spec.source.digest` | `string` | no |  |  | Digest is the OCI manifest digest (sha256:...) at the time of resolution. Used to detect force-pushes (tag overwrites) on the registry. |
| `spec.source.inline` | `boolean` | no |  |  | Inline indicates the contract was provided inline (no external source). |
| `spec.source.oci` | `string` | no |  |  | OCI is the fully resolved OCI reference (including tag/digest). Example: ghcr.io/org/service-pacto:1.2.0 |
| `spec.version` | `string` | yes |  |  | Version is the contract version (from contract.service.version). |
| `status` | `object` | no |  |  | status defines the observed state of PactoRevision. |
| `status.conditions` | `[]object` | no |  |  | conditions represent the current state of the PactoRevision resource. |
| `status.conditions[].lastTransitionTime` | `string` | yes |  |  | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes |  |  | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no |  |  | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes |  |  | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes |  | `True`, `False`, `Unknown` | status of the condition, one of True, False, Unknown. |
| `status.conditions[].type` | `string` | yes |  |  | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.contractHash` | `string` | no |  |  | ContractHash is the SHA-256 hash of the raw contract YAML. Used to detect content changes across versions. |
| `status.createdAt` | `string` | no |  |  | CreatedAt is the timestamp when this revision was first resolved. |
| `status.resolved` | `boolean` | yes |  |  | Resolved indicates whether this revision has been successfully resolved and parsed. |


## Pacto

- **API group**: `pacto.trianalab.io`
- **Version**: `v1alpha1`
- **Kind**: `Pacto`
- **Plural**: `pactos`
- **Scope**: Namespaced

### Additional printer columns

| Name | Type | JSONPath |
| --- | --- | --- |
| Status | string | `.status.contractStatus` |
| Service | string | `.spec.target.serviceName` |
| Version | string | `.status.contractVersion` |
| Errors | integer | `.status.summary.errorCount` |
| Warnings | integer | `.status.summary.warningCount` |
| Last Reconciled | date | `.status.lastReconciledAt` |
| Age | date | `.metadata.creationTimestamp` |

### Fields

| Field | Type | Required | Default | Enum | Description |
| --- | --- | --- | --- | --- | --- |
| `spec` | `object` | yes |  |  | spec defines the desired state of Pacto. |
| `spec.checkIntervalSeconds` | `integer` | no | `300` |  | CheckIntervalSeconds controls how often the reconciler re-checks compliance. Defaults to 300 (5 minutes). |
| `spec.contractRef` | `object` | yes |  |  | ContractRef specifies where to find the Pacto contract. |
| `spec.contractRef.inline` | `string` | no |  |  | Inline allows specifying the contract YAML directly (for testing/dev). |
| `spec.contractRef.oci` | `string` | no |  |  | OCI is the OCI registry reference for the contract bundle. Three forms are supported: - Unversioned (ghcr.io/org/service-pacto): tracks the latest semver tag. - Tagged (ghcr.io/org/service-pacto:1.2.3): pinned to that exact tag. - Digest (ghcr.io/org/service-pacto@sha256:...): immutable, exact reference. |
| `spec.contractRef.pullSecretRef` | `string` | no |  |  | PullSecretRef is the name of a Secret in the same namespace containing OCI registry credentials. Supported secret types: - Opaque with "token" key (bearer token) - Opaque with "username"+"password" keys (basic auth) - kubernetes.io/dockerconfigjson (standard Docker registry auth) For Opaque secrets, add a "registry" key (e.g. "ghcr.io") to bind the credentials to that host; the operator then refuses to send them to any other registry. Recommended to prevent a contract from redirecting a referenced secret to an attacker-controlled registry. |
| `spec.overrides` | `object` | no |  |  | Overrides specifies partial configuration overrides to apply on top of the resolved contract. This enables environment-specific tuning without duplicating the entire contract inline. Semantics mirror the Pacto CLI --set / -f override model. |
| `spec.overrides.configurations` | `[]object` | no |  |  | Configurations lists per-scope configuration value overrides. Each entry is matched by name to a configurations[] entry in the resolved contract. |
| `spec.overrides.configurations[].name` | `string` | yes |  |  | Name identifies the configuration scope to override (must match a configurations[] entry in the contract). |
| `spec.overrides.configurations[].values` | `object` | yes |  |  | Values contains the configuration key-value pairs to merge into the resolved contract. These values take precedence over the values declared in the contract. |
| `spec.target` | `object` | no |  |  | Target specifies which Kubernetes resources to observe. When omitted, the Pacto acts as a reference-only contract (no runtime validation). |
| `spec.target.configBindings` | `[]object` | no |  |  | ConfigBindings maps contract configurations to the concrete ConfigMap/Secret backing each (B7). |
| `spec.target.configBindings[].configuration` | `string` | yes |  |  | Configuration is the contract configurations[].name this binding backs. |
| `spec.target.configBindings[].format` | `string` | no |  | `yaml`, `json` | Format of the value at Key. Required with Key for ConfigMap conformance. |
| `spec.target.configBindings[].key` | `string` | no |  |  | Key names the single ConfigMap/Secret key to decode and validate. Omit for existence-only. |
| `spec.target.configBindings[].kind` | `string` | yes |  | `ConfigMap`, `Secret` | Kind of the backing object. |
| `spec.target.configBindings[].name` | `string` | yes |  |  | Name of the backing ConfigMap/Secret in the Pacto's namespace. |
| `spec.target.interfaceBindings` | `[]object` | no |  |  | InterfaceBindings maps contract interfaces to the Service port that serves each (B4). Kubernetes deployment knowledge — lives on the CR, NOT the platform-agnostic contract. |
| `spec.target.interfaceBindings[].interface` | `string` | yes |  |  | Interface is the contract interfaces[].name this binding resolves. |
| `spec.target.interfaceBindings[].servicePort` | `object` | yes |  |  | ServicePort is the Service port name or number that serves the interface. |
| `spec.target.serviceName` | `string` | no |  |  | ServiceName is the name of the Kubernetes Service to observe. |
| `spec.target.workloadRef` | `object` | no |  |  | WorkloadRef identifies the workload (Deployment, StatefulSet, ReplicaSet, Job, or CronJob). If omitted, defaults to name=serviceName, kind=Deployment. |
| `spec.target.workloadRef.kind` | `string` | no |  | `Deployment`, `StatefulSet`, `ReplicaSet`, `Job`, `CronJob` | Kind of the workload resource. Left unspecified (empty) unless the author sets it explicitly; WORKLOAD_MISMATCH only fires when BOTH name AND kind were explicit (AR7). No default is applied here so the collector can distinguish "author asserted kind X" from "kind was defaulted for the GET". |
| `spec.target.workloadRef.name` | `string` | yes |  |  | Name of the workload resource. |
| `status` | `object` | no |  |  | status defines the observed state of Pacto. |
| `status.capabilities` | `[]object` | no |  |  | Capabilities lists the declared capabilities from the contract. |
| `status.capabilities[].ref` | `string` | no |  |  | Ref is the capability reference (OCI URI or local path). |
| `status.capabilities[].type` | `string` | yes |  |  | Type is the capability type (e.g. "database", "cache"). |
| `status.conditions` | `[]object` | no |  |  | Conditions represent aggregated state (ContractValid, RuntimeObserved, etc.). |
| `status.conditions[].lastTransitionTime` | `string` | yes |  |  | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes |  |  | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no |  |  | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes |  |  | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes |  | `True`, `False`, `Unknown` | status of the condition, one of True, False, Unknown. |
| `status.conditions[].type` | `string` | yes |  |  | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.configurations` | `[]object` | no |  |  | Configurations lists the contract's named configuration scopes. Each entry corresponds to one configurations[] entry in the contract. |
| `status.configurations[].hasSchema` | `boolean` | yes |  |  | HasSchema indicates whether a JSON Schema file is bundled. |
| `status.configurations[].name` | `string` | yes |  |  | Name is the configuration scope name (required, unique within contract). |
| `status.configurations[].overriddenKeys` | `[]string` | no |  |  | OverriddenKeys lists configuration keys whose values were overridden by spec.overrides.configurations. Empty when no overrides apply. |
| `status.configurations[].properties` | `[]object` | no |  |  | Properties lists the configuration's declared keys with their type and default/value, extracted from the bundled schema (or literal values), so consumers can render configuration content without re-reading the bundle. |
| `status.configurations[].properties[].key` | `string` | yes |  |  | Key is the property name (dot-notation for nested objects). |
| `status.configurations[].properties[].type` | `string` | no |  |  | Type is the JSON Schema type of the property. |
| `status.configurations[].properties[].value` | `string` | no |  |  | Value is the default (for schema properties) or the literal value. |
| `status.configurations[].ref` | `string` | no |  |  | Ref is the external OCI reference for the configuration schema, if used. |
| `status.configurations[].secretKeys` | `[]string` | no |  |  | SecretKeys lists configuration keys whose values reference secrets. |
| `status.configurations[].valueKeys` | `[]string` | no |  |  | ValueKeys lists the declared configuration value keys. |
| `status.contract` | `object` | no |  |  | Contract exposes parsed contract metadata. |
| `status.contract.owner` | `object` | no |  |  | Owner contains the structured ownership metadata from the contract (team, DRI, and contacts). |
| `status.contract.owner.contacts` | `[]object` | no |  |  | Contacts lists provider-neutral contact points. |
| `status.contract.owner.contacts[].purpose` | `string` | no |  |  | Purpose describes what this contact is used for (e.g. escalation, support, oncall). |
| `status.contract.owner.contacts[].type` | `string` | yes |  |  | Type is the contact channel type (e.g. email, chat, oncall). |
| `status.contract.owner.contacts[].value` | `string` | yes |  |  | Value is the contact address or identifier. |
| `status.contract.owner.dri` | `string` | no |  |  | DRI is the directly responsible individual. |
| `status.contract.owner.team` | `string` | no |  |  | Team is the owning team name. |
| `status.contract.ownerDisplay` | `string` | no |  |  | OwnerDisplay is the canonical display string derived from owner metadata. Precedence: team > DRI. Useful for printer columns, dashboards, and backward-compatible consumers. |
| `status.contract.resolvedRef` | `string` | no |  |  | ResolvedRef is the fully-resolved OCI reference (with tag/digest). Empty for inline contracts. |
| `status.contract.serviceName` | `string` | yes |  |  | ServiceName is the service name declared in the contract. |
| `status.contract.version` | `string` | yes |  |  | Version is the semver version from the contract. |
| `status.contractStatus` | `string` | no |  | `Compliant`, `Warning`, `NonCompliant`, `Reference`, `Unknown`, `Invalid`, `NotEvaluated` | ContractStatus is the high-level contract compliance state. This reflects contract validation/compliance and is NOT runtime health. |
| `status.contractVersion` | `string` | no |  |  | ContractVersion is the version from the parsed contract. Kept for backward compatibility and simple access via JSONPath. |
| `status.currentRevision` | `string` | no |  |  | CurrentRevision is the name of the active PactoRevision. |
| `status.dependencies` | `[]object` | no |  |  | Dependencies lists the declared dependencies from the contract. |
| `status.dependencies[].compatibility` | `string` | no |  |  | Compatibility is the semver constraint for the dependency. |
| `status.dependencies[].name` | `string` | yes |  |  | Name is the dependency name (required, unique within contract). |
| `status.dependencies[].ref` | `string` | yes |  |  | Ref is the dependency reference (OCI URI). |
| `status.dependencies[].required` | `boolean` | yes |  |  | Required indicates whether this dependency is mandatory. |
| `status.evaluatedRevision` | `object` | no |  |  | EvaluatedRevision ties this status to the exact contract and workload generation it was computed from, so a result for an old version is never read as the answer for a new one. A consumer acting on contractStatus (the opt-in Pacto delivery promoter) must check it first. |
| `status.evaluatedRevision.contractDigest` | `string` | no |  |  | ContractDigest is the OCI manifest digest of the evaluated contract bundle. Empty for inline contracts. |
| `status.evaluatedRevision.kernelCoverage` | `string` | no |  | `Complete`, `Partial`, `Failed`, `Stale` | KernelCoverage is how far the node agent's view of the workload's pods could be trusted in this evaluation, the worst pod first: Complete, Partial, Failed or Stale. Empty when kernel evidence is off. Complete is what lets a reader tell "observed and clean" from "not observed". |
| `status.evaluatedRevision.workload` | `object` | no |  |  | Workload is the workload object and generation the evaluation read. Absent for a reference-only contract or a workload that does not exist. |
| `status.evaluatedRevision.workload.generation` | `integer` | yes |  |  |  |
| `status.evaluatedRevision.workload.kind` | `string` | yes |  |  |  |
| `status.evaluatedRevision.workload.name` | `string` | yes |  |  |  |
| `status.evaluatedRevision.workload.uid` | `string` | yes |  |  |  |
| `status.evaluationCoverage` | `object` | no |  |  | EvaluationCoverage reports how many required assertions were evaluated (metadata; never affects ContractStatus). |
| `status.evaluationCoverage.evaluated` | `integer` | yes |  |  | Evaluated is the number of required assertions with a conclusive (Observed) observation. |
| `status.evaluationCoverage.required` | `integer` | yes |  |  | Required is the total number of required assertions the contract declares. |
| `status.evaluationCoverage.unverified` | `[]string` | no |  |  | Unverified lists, sorted, the contract paths of declared facets that have a runtime check but no usable fact this time (for example interfaces[name=api].type with probing off). Metadata only: it never changes the contract status. |
| `status.findings` | `[]object` | no |  |  | Findings is the list of typed conclusions from the Pacto engine. Includes both contract-only findings (structural/semantic) and evidence-based findings (runtime drift). |
| `status.findings[].category` | `string` | yes |  |  | Category groups related codes (e.g. RuntimeDrift, PolicyViolation). |
| `status.findings[].code` | `string` | yes |  |  | Code is the stable finding identifier (e.g. WORKLOAD_MISMATCH). |
| `status.findings[].contractPath` | `string` | no |  |  | ContractPath is the YAML path to the relevant contract field. |
| `status.findings[].evidenceRefs` | `[]object` | no |  |  | EvidenceRefs links the finding to supporting evidence. |
| `status.findings[].evidenceRefs[].collector` | `string` | no |  |  | Collector names the producer of the matched observation: k8s-observer for the Kubernetes API alone, k8s-observer+ebpf when kernel evidence was merged in, ebpf when only the kernel saw it. |
| `status.findings[].evidenceRefs[].detectedAt` | `string` | no |  |  | DetectedAt is the RFC 3339 time the matched observation was made. |
| `status.findings[].evidenceRefs[].observedAt` | `string` | yes |  |  | ObservedAt is the ISO8601 timestamp when the evidence was collected. |
| `status.findings[].evidenceRefs[].source` | `string` | yes |  |  | Source is the evidence source (e.g. "k8s"). |
| `status.findings[].message` | `string` | yes |  |  | Message is a human-readable description. |
| `status.findings[].severity` | `string` | yes |  | `error`, `warning`, `info`, `unknown` | Severity is error, warning, info, or unknown. |
| `status.findings[].subject` | `string` | no |  |  | Subject identifies what the finding is about. |
| `status.interfaces` | `[]object` | no |  |  | Interfaces lists the parsed interfaces from the contract. |
| `status.interfaces[].name` | `string` | yes |  |  | Name is the interface name. |
| `status.interfaces[].ref` | `string` | no |  |  | Ref is the bundle-relative path to the interface specification file. |
| `status.interfaces[].type` | `string` | yes |  | `openapi`, `asyncapi`, `grpc` | Type is the interface type: openapi, asyncapi, or grpc. |
| `status.interfaces[].visibility` | `string` | no |  |  | Visibility is the declared visibility: public or internal. |
| `status.lastReconciledAt` | `string` | no |  |  | LastReconciledAt is when the last reconciliation completed. |
| `status.metadata` | `object` | no |  |  | Metadata contains arbitrary key-value pairs from the contract's metadata section. |
| `status.observationWindows` | `[]object` | no |  |  | ObservationWindows tracks per-assertion negative-observation streaks for time-based stabilization (B5). Operator-owned temporal state; the pure engine never sees it. |
| `status.observationWindows[].firstObservedNegativeAt` | `string` | yes |  |  | FirstObservedNegativeAt is when the current negative streak began. |
| `status.observationWindows[].kind` | `string` | yes |  |  | Kind is the assertion dimension: interface \| dependency \| configuration \| capability. |
| `status.observationWindows[].subject` | `string` | yes |  |  | Subject is the assertion identity (interface/dependency/configuration name, or capability key). |
| `status.observedGeneration` | `integer` | no |  |  | ObservedGeneration is the most recent generation observed by the controller. |
| `status.observedRuntime` | `object` | no |  |  | ObservedRuntime describes the actual runtime state observed from the cluster. Only populated when a target workload exists. This is a lean evidence view. |
| `status.observedRuntime.containerImages` | `[]string` | no |  |  | ContainerImages lists the container images from the pod spec. |
| `status.observedRuntime.deploymentStrategy` | `string` | no |  |  | DeploymentStrategy is the observed strategy (RollingUpdate, Recreate). Empty for non-Deployments. |
| `status.observedRuntime.hasEmptyDir` | `boolean` | yes |  |  | HasEmptyDir indicates whether the workload uses emptyDir volumes. |
| `status.observedRuntime.hasPVC` | `boolean` | yes |  |  | HasPVC indicates whether the workload uses PersistentVolumeClaims. |
| `status.observedRuntime.healthProbeInitialDelaySeconds` | `integer` | no |  |  | HealthProbeInitialDelaySeconds is the observed initialDelaySeconds from the first container's probe. |
| `status.observedRuntime.podManagementPolicy` | `string` | no |  |  | PodManagementPolicy is the observed pod management policy (OrderedReady, Parallel). Empty for non-StatefulSets. |
| `status.observedRuntime.terminationGracePeriodSeconds` | `integer` | no |  |  | TerminationGracePeriodSeconds is the observed terminationGracePeriodSeconds from the pod spec. |
| `status.observedRuntime.workloadKind` | `string` | no |  |  | WorkloadKind is the actual Kubernetes resource kind (Deployment, StatefulSet, ReplicaSet, Job, CronJob). |
| `status.policies` | `[]object` | no |  |  | Policies lists the contract's declared policy sources (metadata only). Each entry describes a local schema or external ref; the operator does not resolve or enforce ref-based policies at runtime. |
| `status.policies[].description` | `string` | no |  |  | Description is the policy schema's declared description, if any. |
| `status.policies[].hasSchema` | `boolean` | yes |  |  | HasSchema indicates whether a policy schema file is bundled. |
| `status.policies[].name` | `string` | yes |  |  | Name is the policy name (required, unique within contract). |
| `status.policies[].properties` | `[]object` | no |  |  | Properties lists the policy schema's keys with their type and default, so consumers can render policy content without re-reading the bundle. |
| `status.policies[].properties[].key` | `string` | yes |  |  | Key is the property name (dot-notation for nested objects). |
| `status.policies[].properties[].type` | `string` | no |  |  | Type is the JSON Schema type of the property. |
| `status.policies[].properties[].value` | `string` | no |  |  | Value is the default (for schema properties) or the literal value. |
| `status.policies[].ref` | `string` | no |  |  | Ref is the external OCI reference for the policy schema, if used. |
| `status.policies[].schema` | `string` | no |  |  | Schema is the bundle-relative path to the policy schema file, if local. |
| `status.policies[].title` | `string` | no |  |  | Title is the policy schema's declared title, if any. |
| `status.readiness` | `object` | no |  |  | Readiness is the derived operational readiness assessment of the contract. It is computed from the contract's declared readiness claims and the current time. It is a separate dimension from contract compliance and does NOT affect ContractStatus. Absent when the contract declares no readiness. |
| `status.readiness.claims` | `[]object` | no |  |  | Claims is the derived per-claim readiness status. |
| `status.readiness.claims[].category` | `string` | no |  |  | Category is the software-domain category (security, documentation, observability, etc.). |
| `status.readiness.claims[].description` | `string` | no |  |  | Description is the optional human-readable explanation. |
| `status.readiness.claims[].earnedWeight` | `integer` | no |  |  | EarnedWeight is the weight this check contributed toward the score. |
| `status.readiness.claims[].evidence` | `string` | no |  |  | Evidence is the declared pointer to the evidence. |
| `status.readiness.claims[].excluded` | `boolean` | no |  |  | Excluded reports whether the check is excluded from scoring (deferred). |
| `status.readiness.claims[].id` | `string` | yes |  |  | ID is the readiness requirement identifier (e.g. dashboard, runbook). |
| `status.readiness.claims[].status` | `string` | yes |  | `done`, `partial`, `not-done`, `deferred` | Status is the declared completion status. |
| `status.readiness.claims[].type` | `string` | yes |  |  | Type classifies the evidence pointer (url, document, ticket, report, artifact, identifier, other). |
| `status.readiness.claims[].weight` | `integer` | yes |  |  | Weight is the declared contribution to the readiness score (0-100). |
| `status.readiness.daysRemaining` | `integer` | no |  |  | DaysRemaining is the number of whole days until expiry (nil when expired). |
| `status.readiness.deferredCount` | `integer` | no |  |  | DeferredCount is the number of checks declared deferred (excluded from scoring). |
| `status.readiness.doneCount` | `integer` | no |  |  | DoneCount is the number of checks declared done. |
| `status.readiness.earnedWeight` | `integer` | no |  |  | EarnedWeight is the weight earned toward the score. |
| `status.readiness.expired` | `boolean` | no |  |  | Expired reports whether the assessment has expired; when true every in-scope check earns 0. |
| `status.readiness.expires` | `string` | no |  |  | Expires is the assessment-level expiry boundary (YYYY-MM-DD). |
| `status.readiness.minScore` | `integer` | no |  |  | MinScore is the gate threshold (the declared readiness.minScore, or 100 when omitted). |
| `status.readiness.notDoneCount` | `integer` | no |  |  | NotDoneCount is the number of checks declared not-done. |
| `status.readiness.partialCount` | `integer` | no |  |  | PartialCount is the number of checks declared partial. |
| `status.readiness.passing` | `boolean` | no |  |  | Passing reports whether the gate is met (not expired and Score >= MinScore). |
| `status.readiness.revisions` | `[]object` | no |  |  | Revisions is the declared readiness revision history. |
| `status.readiness.revisions[].author` | `string` | yes |  |  | Author is who performed the assessment. |
| `status.readiness.revisions[].date` | `string` | yes |  |  | Date is the date the revision was assessed (YYYY-MM-DD). |
| `status.readiness.revisions[].description` | `string` | yes |  |  | Description summarizes what changed or was observed. |
| `status.readiness.revisions[].version` | `string` | yes |  |  | Version is the service version assessed in this revision. |
| `status.readiness.score` | `integer` | no |  |  | Score is the percentage of in-scope weight earned (0-100). |
| `status.readiness.totalWeight` | `integer` | no |  |  | TotalWeight is the sum of in-scope (non-deferred) check weights. |
| `status.resolutionPolicy` | `string` | no |  | `Latest`, `PinnedTag`, `PinnedDigest` | ResolutionPolicy describes how the OCI reference was resolved. Latest: unversioned ref, operator tracks the highest semver tag. PinnedTag: ref includes an explicit tag, used as-is. PinnedDigest: ref includes a digest, used as-is (immutable). Empty for inline contracts. |
| `status.resources` | `object` | no |  |  | Resources describes the existence of target Kubernetes resources. |
| `status.resources.service` | `object` | no |  |  | Service describes the target Service. |
| `status.resources.service.exists` | `boolean` | yes |  |  | Exists indicates whether the resource was found in the cluster. |
| `status.resources.service.kind` | `string` | no |  |  | Kind of the resource (only set for workloads). |
| `status.resources.service.name` | `string` | yes |  |  | Name of the resource. |
| `status.resources.workload` | `object` | no |  |  | Workload describes the target workload (Deployment/StatefulSet/ReplicaSet). |
| `status.resources.workload.exists` | `boolean` | yes |  |  | Exists indicates whether the resource was found in the cluster. |
| `status.resources.workload.kind` | `string` | no |  |  | Kind of the resource (only set for workloads). |
| `status.resources.workload.name` | `string` | yes |  |  | Name of the resource. |
| `status.summary` | `object` | no |  |  | Summary provides severity-based finding counts. |
| `status.summary.errorCount` | `integer` | yes |  |  | ErrorCount is the number of error-severity findings. |
| `status.summary.infoCount` | `integer` | yes |  |  | InfoCount is the number of info-severity findings. |
| `status.summary.unknownCount` | `integer` | yes |  |  | UnknownCount is the number of unknown-severity findings (a required assertion could not be evaluated). |
| `status.summary.warningCount` | `integer` | yes |  |  | WarningCount is the number of warning-severity findings. |
| `status.validation` | `object` | no |  |  | Validation describes the structural validation outcome of the contract. |
| `status.validation.errors` | `[]object` | no |  |  | Errors lists structural validation errors. |
| `status.validation.errors[].code` | `string` | no |  |  | Code is a machine-readable error code. |
| `status.validation.errors[].message` | `string` | yes |  |  | Message is a human-readable description of the issue. |
| `status.validation.errors[].path` | `string` | no |  |  | Path is the JSON path to the invalid field. |
| `status.validation.valid` | `boolean` | yes |  |  | Valid indicates whether the contract passed structural validation. |
| `status.validation.warnings` | `[]object` | no |  |  | Warnings lists structural validation warnings. |
| `status.validation.warnings[].code` | `string` | no |  |  | Code is a machine-readable error code. |
| `status.validation.warnings[].message` | `string` | yes |  |  | Message is a human-readable description of the issue. |
| `status.validation.warnings[].path` | `string` | no |  |  | Path is the JSON path to the invalid field. |
