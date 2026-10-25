---
"@pacto/k8s-module": minor
---

Add the Pacto delivery promoter and recorder, an opt-in component with its own
image, chart and API group, and tie every Pacto check result to the version it
evaluated. The `pacto-operator` chart and image gain no permission.

**Pacto status.** `Pacto.status.evaluatedRevision` names the contract digest
and the workload object and generation an evaluation read, and its
`kernelCoverage` says how far the node agent's view could be trusted
(`Complete`, `Partial`, `Failed` or `Stale`). A consumer acting on
`contractStatus` must check `evaluatedRevision` first, as the promoter does.
The Pacto status also accepts `mcp` interfaces, so agent contracts reconcile.

**The API.** `delivery.pacto.trianalab.io/v1alpha1` adds `Stage` (one service
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
`identities.teamGroupPrefix`); a question that names no team is answered by
nobody unless `identities.requireOwnerTeam` is false, and with
`identities.humanGroups` set only those groups count as people. The chart
publishes these identities in the ConfigMap `pacto-delivery-identities`, which
the promoter runs with and the CLI, the MCP server and the dashboard read. It takes the Stage lock with one
compare-and-swap, moves the pointer, waits for the apply and runs the verify
checks the Stage lists in `spec.gates.verify.checks`: the Pacto contract check,
providers from `gates.providers` (`mira`, `http` or `eval`) and the built-in
`pacto.runtime`. Checks run again every `spec.gates.verify.recheck` after
release. Unknown never passes and never rolls back. A confirmed failure is
never promoted again (the Stage keeps its last 50) and gets one bound rollback;
a second failure, a version a person chose, nothing safe to go back to, a
refused automatic rollback, an outcome
that cannot be established or an edit of the pointer the promoter did not make
freezes the Stage. Stages with `auto: true` promote the newest eligible
candidate.

**Reach.** A Stage applies through a Flux `OCIRepository` by default, or
through an Argo CD Application with an OCI source (`applyRef.kind:
Application`, chart `argocd.enabled`). `candidates.upstreamRecords` lets a
Stage follow a Stage in another cluster from that cluster's signed records,
read with `promoter.upstreamTrust`; without it the Stage is not Ready
(`UpstreamUntrusted`).

**The recorder** (subcommand `recorder`, chart `recorder.*`, off by default)
is the only holder of the signing key and the registry push credentials. It
writes a signed record of every operation that ends, in order per Stage,
attaches it to the versions and contract involved, confirms the Referrers API
lists it and only then advances the Stage's stream. A record can be late, never
skipped. It signs with an Ed25519 key, a KMS key or keyless through a projected
service account token, verifies each stream against its trust config before
extending it, freezes a Stage whose stream is in conflict and can post a
CDEvent per record. `RecordStoreReady` says whether records are being written,
and `pacto_delivery_records_pending` shows the lag. With the recorder on, the
promoter deletes recorded operations after `recorder.operationTTL` (a week),
never the one a Stage runs.

**The `pacto-delivery` chart**
(`oci://ghcr.io/trianalab/pacto/charts/pacto-delivery`) installs both with
their CRDs, ServiceAccounts, RBAC, the `pacto-delivery-agent` and
`pacto-delivery-approver` roles and ValidatingAdmissionPolicies (Kubernetes
1.30 or later) that bind an operation's actor to the requesting user, keep
approvals to people with a login time, keep the pointer to the promoter or
break-glass, keep the promoter itself to the pointer field and keep the record
fields to the recorder. The chart renders them even offline and refuses to
render without them unless `admissionPolicies.allowUnguarded` is set; the
promoter admits no operation and counts no approval while they are missing.
`promoter.registryCredentials.secretName` lets the promoter read a private
contract or deploy repository. The image and chart are published, signed and
versioned with the operator, so install the delivery chart at your operator's
version. Helm never updates a chart's CRDs: apply the
delivery CRDs before every `helm upgrade pacto-delivery`. `make sync-crds`
copies each API group into its own chart.

**The `pacto-operator` chart** gains `dashboard.auth.oidc` (`issuerURL`,
`clientID`, `clientSecretRef`, `redirectURL`, `extraScopes`),
`dashboard.auth.required`, `dashboard.auth.deliveryNamespace` and
`dashboard.miraURL` for the managed dashboard's sign-in. The dashboard's role gains no delivery right and no
impersonation. The operator now passes the managed dashboard its namespace
scope in `PACTO_DASHBOARD_NAMESPACE`, the variable the dashboard reads.
