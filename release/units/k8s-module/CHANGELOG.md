# @pacto/k8s-module

## 5.4.1

### Patch Changes

- 35c37f6: Carry the checksums for the pinned engine core in the operator's `go.sum`.
  
  The published `v5.4.0` module requires `github.com/trianalab/pacto/v3 v3.3.0`
  while its `go.sum` only covers `v3.2.7`, so building the operator module on
  its own — outside the workspace, which is how a consumer or a fresh clone
  builds it — fails with `missing go.sum entry` for every `pacto/v3` package.
  The fix landed on main in #369; this release ships it, and moves the
  published pin to `v3.3.1`.

## 5.4.0

### Minor Changes

- 771574b: Make the operator's status tell the truth about overrides, force-pushed tags and
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

## 5.3.0

### Minor Changes

- 92a064f: Make the contract verdict usable as a GitOps promotion gate, and emit the event
  that says a contract recovered.
  
  The operator has always reached a verdict and written it to
  `status.contractStatus`. Neither Flux nor Argo CD reads that field. Flux decides
  health with kstatus, which recognises `Ready`, `Reconciling` and `Stalled` and
  discards every condition Pacto publishes, so a Kustomization holding a
  `NonCompliant` Pacto reports healthy. Argo picks health checks from a closed list
  of built-in kinds and returns nothing for the rest, and the roll-up ignores
  nothing — a violated contract is not unhealthy to Argo, it is invisible. Both
  tools have an extension point for this; neither ships one for Pacto.
  
  The new [GitOps promotion gates](integrations/kubernetes/gitops.md) page is those
  two snippets, plus the timing they depend on. The Flux one is a
  `spec.healthCheckExprs` entry with no `inProgress` expression, so an unrecognised
  verdict holds the deploy instead of going falsely green. The Argo one is a Lua
  health customization written for the sandbox Argo actually runs it in, which has
  the string library disabled.
  
  Neither snippet is an illustration. Both live under
  `tests/acceptance/kind/fixtures/gitops/`, the page includes those files rather
  than copies of them, and each has a kind acceptance shard that applies it to a
  real cluster running the tool it targets.
  
  `gitops-flux.sh` proves the Flux gate changes what ships: a contract that
  contradicts the workload must leave the dependent Kustomization's manifest out of
  the cluster entirely, and correcting the contract must let it through. It runs at
  the operator's default stabilization window on purpose, because the page claims a
  mismatch does not wait one out.
  
  `gitops-argocd.sh` runs in two passes, because the Argo snippet's failure mode is
  silence — a `data` key Argo does not recognise is ignored, and an ignored key
  looks exactly like having configured nothing. The first pass needs no cluster:
  the `argocd` CLI evaluates the customization in the same Lua sandbox the
  controller uses, which is what gives every contract status an assertion,
  including the states a running cluster passes through too quickly to catch — no
  status yet, a verdict behind the contract, a status added in some future release.
  The second pass serves an Application from an OCI source in kind and requires it
  to go `Degraded` naming the finding, then `Healthy` once the contract is
  corrected. The page's read-back recipe is that same CLI command, and the shard
  runs it against the live cluster rather than only publishing it.
  
  Building that second pass turned up something the page has to say out loud: the
  merge patch alone is not enough. Argo's application controller caches health
  customizations at startup and compares that cached verdict to decide whether a
  changed object is worth re-examining, so a controller that started without the
  customization treats every verdict the operator writes as no change and only
  catches up on the next periodic resync, minutes later. The page now pairs the
  patch with a controller restart, and says why the two read-back checks cannot
  detect the difference — both read the ConfigMap, not the controller.
  
  `ContractRecovered` is new. The three contract warnings — `ValidationFailed`,
  `ContractInvalid`, `ContractUnavailable` — are transition-gated, so a contract
  that goes back to `Compliant` used to fall silent with no event marking the
  recovery. It now has one, matching the pair that `ReadinessGateUnmet` and
  `ReadinessRecovered` already formed. `ValidationFailed` also fired only when
  `status.summary` happened to be set; it now fires on the transition itself.
  
  Two documentation corrections ride along, both load-bearing for the timing advice
  on the new page. The stabilization window applies to **absences only** —
  `INTERFACE_ABSENT`, `DEPENDENCY_UNREACHABLE`, `CAPABILITY_ABSENT` and
  `CONFIGURATION_ABSENT`. A **mismatch** — `WORKLOAD_MISMATCH`,
  `PERSISTENCE_MISMATCH`, `CONFIGURATION_MISMATCH` — is `NonCompliant` on the first
  reconcile that observes it. The events table said seven events and listed seven;
  there are eight.

## 5.2.4

### Patch Changes

- 840a183: Move both Go modules onto the Kubernetes 0.37.0 library line and patch the
  runtime image's OpenSSL.
  
  `k8s.io/api`, `k8s.io/apimachinery` and `k8s.io/client-go` are now v0.37.0 in
  `go.mod` and `integrations/kubernetes/go.mod`, together with the transitive
  `k8s.io/kube-openapi`, `k8s.io/utils`, `k8s.io/streaming`,
  `sigs.k8s.io/structured-merge-diff/v6` and `go-openapi/swag` moves the line
  pulls in. The three library modules only work in lockstep, so bumping them
  one at a time — as the individual Dependabot pull requests did — leaves
  `k8s.io/api` behind and fails to compile.
  
  The runtime stage of the CLI/dashboard image now runs `apk upgrade` before
  installing its packages, so the image picks up the fixed `libssl3`/`libcrypto3`
  3.5.8-r0 instead of the 3.5.7-r0 baked into the `alpine:3.22` tag (CVE-2026-14456,
  HIGH).
  
  No API or behaviour change.

## 5.2.3

### Patch Changes

- b11de31: Move both Go modules onto the Kubernetes 0.36.4 library line.
  
  `k8s.io/client-go`, `k8s.io/api` and `k8s.io/apimachinery` are now v0.36.4 in
  `go.mod` and `integrations/kubernetes/go.mod`. The bumps landed on `main`
  without a changeset, so neither the core line nor the kubernetes line would
  have shipped them — this patch is what actually publishes a core module and an
  operator image built against 0.36.4.
  
  No API or behaviour change: the 0.36.4 patch releases only refresh the
  `golang.org/x` dependencies underneath.

## 5.2.2

### Patch Changes

- 2ea7dbe: Rebuild the operator on pacto core v3.2.2.
  
  The 3.2.2 Version PR bumped `integrations/kubernetes/go.mod` to
  `github.com/trianalab/pacto/v3 v3.2.2`, but a core-line release does not
  republish the kubernetes line, so the operator image and chart stayed on the
  code built against v3.2.1. This changeset moves the kubernetes fixed group to
  5.2.2 so the published operator actually carries:
  
  - one `FILE_NOT_FOUND` finding per missing interface spec file, and a directory
    at a spec path no longer passing as a file
  - date-like scalars kept verbatim through generic YAML round-trips, so an
    unquoted timestamp is validated as the text the contract author wrote

## 5.2.1

### Patch Changes

- c230de9: Make a demo-fixture edit unable to half-ship a release.
  
  Release run 32560058692 published four irreversible units and then died. Two
  independent defects had to line up for that, and both are closed here.
  
  The demo bundles publish to immutable tags. `payments-service` 2.1.0 was edited
  in place — a mermaid diagram added to a version already published — so the
  byte-exact gate correctly refused the tag, but it refused it mid-release,
  because nothing ran that gate before the release. The fixture is restored to its
  published bytes and the diagram ships as a new `payments-service` 2.1.1, and
  `publish-demo-bundles.sh --check` now runs the identical gate read-only at PR
  time as the `demo-bundle-immutability` CI leg.
  
  Separately, the `demo-compose` job lost its ORAS install when the unit moved to
  `docker compose publish`, on the reasoning that ORAS stayed where the ledger
  used it — while that job still read and wrote the ledger, which *is* the ORAS
  user. `ledger.sh` returned the empty string for a missing binary, the empty
  string means "nothing recorded", and the unit failed closed. `ledger.sh` now
  refuses to run without its tools and distinguishes a 404 from an unreadable
  registry; the two `if [ "$(ledger.sh …)" ]` call sites that discarded its exit
  status now assign first; and a new gate walks every job's shell through its make
  targets and scripts and fails when a job can reach a CLI it never installed.
  That gate found a second, quieter instance: the release dry run was rehearsing
  without `syft`, silently skipping the SBOM the real release produces.

## 5.2.0

### Minor Changes

- 8352060: Run the operational graph in a cluster: an operator-managed Evidence Server and observed-dependency input for the dashboard.
  
  - **Managed Evidence Server.** Set `evidence.enabled` and the operator reconciles
    a separate Evidence Server Deployment and an internal Service, with optional
    Ingress or Gateway API `HTTPRoute` exposure. It is single-writer, so its replica
    count is fixed at one, and it runs the same runtime image as the managed
    dashboard. `evidence.registry.subjects` names the exact immutable contract
    revisions evidence may be reported against; the registry holding them is the
    durable store, so the chart installs no volume, database or bucket of its own.
  - **Observed dependencies.** `dashboard.observation.sources` mounts offline
    OTLP/JSON trace exports read-only into the managed dashboard, so its
    operational graph can reconcile declared dependencies against observed ones.
    Each entry is one named data source with a stable identity, read through a root
    the process cannot follow a symlink out of, never written to and never scanned.
    This is offline input only: no OTLP receiver ships and no collector is deployed.
  - **`insecureRegistries`.** Reach named `host:port` registries over plain HTTP for
    a controlled in-cluster registry, scoped per host so every other registry stays
    HTTPS-only. The controller, the managed dashboard and the managed Evidence
    Server all inherit it.
  
  Also adds a `pacto-dev-gateway` chart that installs Envoy Gateway and a
  `GatewayClass` for local development, so the Gateway API path can be exercised on
  a laptop cluster.
  
  Backwards compatible: every new capability is off by default and no existing
  value, CRD field or status shape changes.

## 5.1.2

### Patch Changes

- bbc7b9c: Rebuild the operator and dashboard container images through the new native per-arch
  build pipeline: each architecture builds on its own runner (no QEMU emulation) and is
  merged into the multi-arch manifest. No functional change to the engine, operator, or
  dashboard — this release ships and validates the faster image pipeline.

## 5.1.1

### Patch Changes

- dd4dab1: Repo-wide audit remediation (engine, CLI, dashboard). Closes the OpenAPI
  breaking-change diff false-negatives — path-item-level parameters are now diffed,
  the request body is deep-diffed so a newly required property is BREAKING, and
  optional→required / added-required parameters are BREAKING — so a BREAKING-only
  release gate can no longer be bypassed. Further security and dashboard fixes land
  in the same PR.

  As the first release since v3.1.0, this also ships the previously-merged but
  unreleased dashboard **Ctrl+C shutdown fix** and the demo version-label fix.

## 5.1.0

### Minor Changes

- b58778a: Unify all published OCI artifacts under the monorepo `ghcr.io/trianalab/pacto/*` namespace.

  - operator image → `ghcr.io/trianalab/pacto/operator`
  - operator chart → `ghcr.io/trianalab/pacto/charts/pacto-operator`
  - dashboard image → `ghcr.io/trianalab/pacto/dashboard`
  - dashboard contract bundle → `ghcr.io/trianalab/pacto/dashboard-contract`
  - demo bundles already live under `ghcr.io/trianalab/pacto/*`

  All packages are now created and owned by this repository. The chart name
  `pacto-operator` and the Artifact Hub repository are preserved (re-point the AH
  repository URL to the new chart coordinate). The previous coordinates remain as
  historical — their already-published versions are unaffected. Go module paths
  (`/v3`, `/v5`) are unchanged.

## 5.0.0

### Major Changes

- 045f11e: The Kubernetes integration moves into the monorepo — breaking for consumers.

  - The Go module path becomes `github.com/trianalab/pacto/integrations/kubernetes/v5`
    (was `github.com/trianalab/pacto-operator`).
  - It pins the published core module `github.com/trianalab/pacto/v3` at release
    time; the `go.work` workspace resolution is development-only.
  - The integration continues the operator `v4` line as `v5` (image + chart +
    module); public OCI/chart coordinates are preserved.
