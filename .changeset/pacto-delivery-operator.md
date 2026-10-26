---
"@pacto/k8s-module": minor
---

Add the Pacto delivery promoter and recorder, an opt-in component with its own
image and API group, installed by the `pacto-operator` chart under
`delivery.enabled` into its own namespace, and tie every Pacto check result to
the version it evaluated. The operator holds no role in the delivery namespace,
so it cannot run a pod as the promoter or the recorder or mint their tokens.

**Pacto status.** `Pacto.status.evaluatedRevision` names the contract digest
and the workload object and generation an evaluation read, and its
`kernelCoverage` says how far the node agent's view could be trusted
(`Complete`, `Partial`, `Failed` or `Stale`). A consumer acting on
`contractStatus` must check `evaluatedRevision` first, as the promoter does.
The Pacto status also accepts `mcp` interfaces, so agent contracts reconcile.
`status.observedCallers` lists the workloads the node agent saw calling the
target's Service that no Pacto describes, and `status.observedCallersCoverage`
says whether that list is complete, partial (with a reason such as
`shared-netns` or `stale-node`) or unavailable; delivery holds a breaking
change on either.

**Check results change for every operator user**, delivery on or off. A
dependency that resolves to the same service in several namespaces now
resolves to the one in the consumer's own namespace when there is one (a
service in test calls the provider in test), where before the match was
ambiguous (Insufficient) unless exactly one of them satisfied the declared
range. The node agent's evidence leaves out pods being deleted, so a
pod still shutting down no longer counts toward kernel coverage or findings,
and a workload with no running pod has `Partial` kernel coverage.

**The API.** `delivery.pacto.trianalab.io/v1alpha1` (Alpha, in the kubectl
category `pacto-delivery`) adds `Stage` (one service
in one environment, whose pointer is the artifact digest it runs), `Operation`
(one decision, with an immutable spec) and `Approval` (a person's decision
bound to an operation's uid and intent digest and to the approver's groups).

**The promoter** (`ghcr.io/trianalab/pacto/delivery`, subcommand `promoter`)
admits each operation and promotes only a version `pacto publish` published:
the Stage's candidate `OCIRepository` must set `spec.verify` naming the
publisher, and a downstream Stage takes only what its upstream Stage verified
and offered (`status.published`, `status.offered`). Anything else is refused as
`NotPublished`, and a Stage that cannot check its candidates says why in its
`Ready` condition. It then compares the contracts, checks every consumer's
declared range, holds for a person when a consumer in the Stage's environment
would break or Pacto cannot see enough and writes to `status.plan.order` which
consumer must move first, planned again while the change waits. It accepts an
approval only from a person in a team the question names (chart
`delivery.identities.teamGroupPrefix`); a question that names no team is answered by
nobody unless `delivery.identities.requireOwnerTeam` is false, and with
`delivery.identities.humanGroups` set only those groups count as people. The chart
publishes these identities in the ConfigMap `pacto-delivery-identities`, which
the promoter runs with and the CLI, the MCP server and the dashboard read. It takes the Stage lock with one
compare-and-swap, moves the pointer, waits for the apply and runs the verify
checks the Stage lists in `spec.gates.verify.checks`: the Pacto contract check,
providers from `delivery.gates.providers` (`mira`, `http` or `eval`) and the built-in
`pacto.runtime`. Checks run again every `spec.gates.verify.recheck` after
release. Unknown never passes and never rolls back. A confirmed failure is
never promoted again (the Stage keeps its last 50) and gets one bound rollback;
a second failure, a version a person chose, nothing safe to go back to, a
refused automatic rollback, an outcome
that cannot be established or an edit of the pointer the promoter did not make
freezes the Stage. A Stage starts on `spec.startRung` (`observe` by default;
an admin creating a Stage may pick any rung) and only an `autonomy` operation moves `status.rung` after that;
on `auto-nonbreaking` and `autonomous` the promoter promotes the newest
eligible candidate itself. It records what an observe Stage would have done in
`status.shadow` (the newest 20), proposes the next rung in `status.proposal`
after `spec.autonomy.proposeAfter` verified changes (10 by default) and, after
an automatic rollback of a version it shipped on its own, lowers the Stage one
rung through an operation named `<failed operation>-stepdown`. `kubectl get
stages` shows the rung. `spec.auto` is gone. A breaking change no contract depends on also waits for a person,
since a service without a contract may call it, unless the Stage sets
`gates.impact.declaredConsumersOnly`.

The promoter emits Events when it holds, concludes, rolls back or freezes and
exports `pacto_delivery_operations_total`, `pacto_delivery_verdicts_total`,
`pacto_delivery_stage_frozen` and `pacto_delivery_stage_watched` (chart
`delivery.metrics.*`). Each Stage says in `Watched` whether its running version passed
its latest re-check and in `Mediated` whether its pointer alone decides what
runs, gives every condition an observed generation and publishes the
promoter's login freshness in `status.authFreshness`. An approval the promoter
passes over is named on the operation with the reason.

**Reach (experimental).** A Stage applies through a Flux `OCIRepository` by
default, or, as an experimental option, through an Argo CD Application with an
OCI source (`applyRef.kind: Application`, chart `delivery.argocd.enabled`). `candidates.upstreamRecords` lets a
Stage follow a Stage in another cluster from that cluster's signed records,
read with `delivery.promoter.upstreamTrust`; without it the Stage is not Ready
(`UpstreamUntrusted`), and while those records cannot be read or lost their
newest entries it is not Ready either (`UpstreamUnreadable`). Ready also names
an applier the promoter cannot write through. Only a Flux `OCIRepository` with
`spec.verify` admits a new version, so the first Stage of a service needs Flux
even when Argo CD applies it. A freeze is local: it does not stop followers. Argo CD and cross-cluster reach
may change more than the rest of delivery.

**The recorder** (subcommand `recorder`, chart `delivery.recorder.*`, off by default)
is the only delivery component that holds the signing key and the registry
push credentials. With an Ed25519 key the operator, which reads every Secret,
can read the key too, so prefer a KMS key or keyless signing. It
writes a signed record of every operation that ends, in order per Stage,
attaches it to the versions and contract involved, confirms the Referrers API
lists it and only then advances the Stage's stream. A record can be late, never
skipped. It signs with an Ed25519 key, a KMS key or keyless through a projected
service account token, verifies each stream against its trust config before
extending it, freezes a Stage whose stream is in conflict and can post a
CDEvent per record. A record of an autonomy operation carries `outcome.rung`
(`from` and `to`). `RecordStoreReady` says whether records are being written,
and `pacto_delivery_records_pending` shows the lag. With the recorder on, the
promoter deletes recorded operations after `delivery.recorder.operationTTL` (a week),
never the one a Stage runs.

**The `delivery` section of the `pacto-operator` chart** (`delivery.enabled`,
off by default; the default render is unchanged) installs both in
`delivery.namespace` (`pacto-delivery`, created by the chart and never the
release namespace) with the
Stage, Operation and Approval CRDs, their own ServiceAccounts and RBAC, the
`<release>-delivery-agent` and `<release>-delivery-approver` roles and
ValidatingAdmissionPolicies that bind an operation's actor to the requesting
user, keep approvals to people with a login time (the apiserver maps the login
time to `userInfo.extra["delivery.pacto.trianalab.io/auth-time"]`), keep the
pointer to the promoter or break-glass, keep the promoter itself to the pointer
field and keep the record fields to the recorder. Every delivery object is
named `<release>-delivery[-part]`. It refuses to render below Kubernetes 1.30,
without the ValidatingAdmissionPolicy API or without the policies unless
`delivery.admissionPolicies.allowUnguarded` is set; the promoter admits no
operation and counts no approval while they are missing.
`delivery.promoter.registryCredentials.secretName` lets the promoter read a
private contract or deploy repository. The delivery CRDs are chart templates
with `helm.sh/resource-policy: keep`: `helm upgrade` updates them and
`helm uninstall` leaves them and their history in place. The delivery image is
published, signed and versioned with the operator. `make sync-crds` copies the
delivery CRDs into the chart's `files/delivery-crds/`.

**The chart's images** gain `image.digest` and `delivery.image.digest`: set
one to a `sha256:` digest and that image is pulled as `tag@digest`, so the pod
runs exactly that image whatever the tag later points at.

**The `pacto-operator` chart** gains `dashboard.auth.oidc` (`issuerURL`,
`clientID`, `clientSecretRef`, `redirectURL`, `extraScopes`),
`dashboard.auth.required`, `dashboard.auth.deliveryNamespace` and
`dashboard.miraURL` for the managed dashboard's sign-in. The dashboard's role gains no delivery right and no
impersonation. The operator now passes the managed dashboard its namespace
scope in `PACTO_DASHBOARD_NAMESPACE`, the variable the dashboard reads.
