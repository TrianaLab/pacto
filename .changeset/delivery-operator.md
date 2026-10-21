---
"@pacto/k8s-module": minor
---

Add the Pacto delivery promoter, an opt-in component with its own image, chart
and API group, and tie every Pacto check result to the version it evaluated.

`Pacto.status.evaluatedRevision` names the contract digest and the workload
object and generation an evaluation read. `status.contract.resolvedRef` is
tag-form unless you pinned a digest, so it could not say which contract a
verdict belongs to. A consumer acting on `contractStatus` must check it first,
and the delivery promoter does.

The `delivery.pacto.trianalab.io/v1alpha1` API adds `Stage` (one service in one
environment, whose pointer is the artifact digest it runs), `Operation` (one
decision, with an immutable spec) and `Approval` (a person's decision bound to
an operation's uid and intent digest). The promoter (`cmd/delivery`, image
`pacto-delivery`, subcommand `promoter`) admits each operation, runs the
`pacto.diff` and `pacto.impact` pre-gates, holds for a person when a consumer
running in the Stage's environment would break or Pacto cannot see enough (a
consumer counts unless its environment label or the namespace a Stage's
Kustomization applies into places it elsewhere; and
writes to `status.plan.order` which consumer must move first and, when one
exists, the verified version of it to promote), takes the Stage lock with one
compare-and-swap, sets `spec.ref.digest` on the Flux `OCIRepository` the Stage
names, waits for Flux and runs the Pacto contract check as the verify gate.
Unknown never passes. A failed version is never promoted again and is rolled
back by one bound rollback; a second failure or an outcome that cannot be
established freezes the Stage. Stages with `auto: true` promote the newest
eligible candidate, a person's rollback puts an auto-hold on the Stage, and an
edit of the pointer the promoter did not make is recorded as a witnessed
override that freezes the Stage.

The new `pacto-delivery` chart installs the promoter with its own CRDs,
ServiceAccount, RBAC, `pacto-delivery-agent` and `pacto-delivery-approver`
roles and four ValidatingAdmissionPolicies (Kubernetes 1.30 or later): an
operation's actor is the requesting user, only the promoter may raise a
witnessed operation, override and unfreeze are limited to their raisers,
cancel is refused once the pointer moved, approvals come from people with a
login time, and `spec.ref.digest` on a labelled `OCIRepository` is edited only
by the promoter or break-glass. The `pacto-operator` chart and image gain no
permission.

The delivery image (`ghcr.io/trianalab/pacto/delivery`) and chart
(`oci://ghcr.io/trianalab/pacto/charts/pacto-delivery`) are published, signed
and versioned with the operator on every release, so install the delivery
chart at your operator's version. The operator `Dockerfile` takes
`CMD=cmd/delivery` to build the delivery image.
`make sync-crds` now copies each API group into its own chart. Helm never
updates a chart's CRDs, so the delivery page shows how to apply the delivery
CRDs before `helm upgrade pacto-delivery`; the operator upgrade still applies
only its own.
