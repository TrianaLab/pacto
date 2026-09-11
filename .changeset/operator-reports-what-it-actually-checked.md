---
"@pacto/k8s-module": minor
---

Make the operator's status tell the truth about overrides, force-pushed tags and
per-tag failures, and stop the registry fan-out riding on the reconcile loop.

**`spec.overrides` are now validated.** An override patched the parsed contract
struct only, so validation layers 1 and 3 — the JSON Schema check and policy
enforcement — still ran against the original document. An override that violated
a policy reported `ContractValid=True`. The patch now applies to the raw YAML as
well, so every layer sees the contract the operator actually reconciles.

**Force-push detection works on the cases it was missing.** Two of them. A tag
force-pushed twice compared the registry digest against an arbitrary
`PactoRevision` rather than the newest one, so `TagOverwritten` re-fired on every
reconcile forever. And a registry tag of `v1.2.0` for a contract declaring
`service.version: 1.2.0` missed the label lookup entirely, so the drift check
never ran for any v-prefixed tag.

**A tag that fails to load is now reported.** Every per-tag failure during
mirroring was a `log.V(1)` and a `continue`: invisible at default verbosity, and
one bad tag aborted nothing but told nobody. Failures are aggregated and surface
as the same events the main path already uses — `ContractUnavailable` for a
transient obtain failure, `ContractInvalid` for everything else.

**`.status.contractStatus` and the dashboard now agree.** The operator derived
the status ladder from findings with its own local copy of the rule. It calls
`validation.DeriveStatus` instead, so a contract cannot read `Warning` in
`kubectl` and `Compliant` in the dashboard.

**A configuration's schema is read from the bundle.** The runtime config
dimension treated `configuration.schema` as inline JSON when it is a
bundle-relative path, so a real schema never compiled and the dimension reported
insufficient evidence instead of a verdict.

**Tag mirroring runs on its own schedule.** Enumerating a registry's tags and
loading each one used to happen inline in `Reconcile`, which every watched
workload write triggers — so a busy namespace turned into registry traffic
proportional to unrelated churn. It is now a manager runnable on a five-minute
tick, independent of reconciliation.

**Pull Secrets are read straight from the API server.** A cached typed `Get` on
a Secret builds an informer for every Secret in scope and parks its `.data` in
the operator's memory. Secret reads go through the uncached reader and the Secret
watch carries metadata only, so the operator holds no credential it is not using
right now.

The dashboard and Evidence Server components moved to the same runnable shape
behind a shared `component` lifecycle, with the same five-minute tick and an
ownership check before any delete.

Deprecated, with no replacement needed: the condition reasons no reconciler
emits — `NotFound`, `AllPortsMatch`, `MissingPorts`, every `ReasonEndpoint*`,
every runtime-reconciliation reason and the three severity constants. The
outcomes they used to name are reported as evidence and findings now. They stay
through v5 for API compatibility and are removed at v6.
