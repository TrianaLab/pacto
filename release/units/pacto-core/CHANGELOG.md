# @pacto/core

## 3.2.5

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

## 3.2.4

### Patch Changes

- 3987568: Restructure the documentation as one information system.
  
  An editorial and information-architecture pass over the whole surface: the nav
  is ordered as a reader's path, each concept has one canonical home, duplicated
  worked examples and repeated statements of the thesis are gone, and development
  history is out of the product pages. Three pages were split out of pages that
  were carrying two subjects — the Pacto model, dashboard architecture and
  observation sources. The published surface loses about 1,500 words while staying
  roughly the same length in lines: the prose is tighter and the split-out pages
  add the structure back. No technical claim was dropped, and no file changed path,
  so every existing URL still resolves.
  
  Docs-only; no functional change to the engine, CLI or dashboard. This core patch
  is the release that redeploys the site, which `docs.yml` deliberately does not do.

## 3.2.3

### Patch Changes

- b11de31: Move both Go modules onto the Kubernetes 0.36.4 library line.
  
  `k8s.io/client-go`, `k8s.io/api` and `k8s.io/apimachinery` are now v0.36.4 in
  `go.mod` and `integrations/kubernetes/go.mod`. The bumps landed on `main`
  without a changeset, so neither the core line nor the kubernetes line would
  have shipped them — this patch is what actually publishes a core module and an
  operator image built against 0.36.4.
  
  No API or behaviour change: the 0.36.4 patch releases only refresh the
  `golang.org/x` dependencies underneath.

## 3.2.2

### Patch Changes

- 9f27024: Keep date scalars verbatim across generic YAML round-trips.
  
  `pacto_edit` could not edit a pristine `pacto init` scaffold. Edit reads
  pacto.yaml into a `map[string]any`, and yaml.v3 resolves an unquoted
  `readiness.expires: 2099-12-31` to a `time.Time`, so re-encoding wrote
  `2099-12-31T00:00:00Z` and the tool rejected the contract it had just produced.
  The same round-trip happens in `pkg/override` (`pacto pack --set`) and in the
  structural validator, which was handing the JSON Schema layer an RFC3339 string
  for a value the document spells as a bare date.
  
  The three sites now decode through `contract.DecodeYAML`, which retags
  `!!timestamp` scalars as `!!str` before decoding, so the text the author wrote
  survives untouched — the same thing `contract.Parse` has always done by decoding
  dates into string fields. Nothing is reformatted: a non-canonical `2099-1-1`
  stays rejectable instead of being canonicalised by an unrelated edit, an explicit
  `2024-01-15T00:00:00Z` keeps its time instead of being truncated to a date, and
  the schema layer never checks a value that is not in the file.

## 3.2.1

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

## 3.2.0

### Minor Changes

- 8352060: Add the Pacto operational graph: what is declared, what is actually running and how the two differ.
  
  Pacto could describe a contract. It could not describe a fleet. This release adds
  the read model for that, and the surfaces on top of it.
  
  `pkg/fleet` composes many contracts, contract revisions and operational targets
  into an immutable, deterministic `FleetSnapshot` with a pure, network-free
  `Query` over it. It keeps three identities distinct — the logical service, the
  contract revision and the operational target — and it keeps them
  domain-qualified, so two teams may own a `checkout` without becoming one node. It
  makes incompleteness explicit: every snapshot and every answer carries an as-of
  time, a completeness and structured limitations, so an unreachable source is
  reported as an `unavailable` source that turns the answer's `completeness` into
  `partial` — surfacing in the dashboard as `unavailable` knowledge, taken from the
  worst source health — and never as an authoritative empty graph. `unknown` stays
  a distinct state, for when there is no completeness envelope at all.
  
  Around that read model:
  
  - **Evidence reporting.** `pacto evidence serve` accepts signed evidence sets
    from environments Pacto cannot reach, verifies the producer signature, checks
    the report against the resolved contract revision and records the result. An
    environment that stops reporting goes stale rather than disappearing.
    `pkg/evidenceenvelope` is the signed wire format and `pkg/evidenceingest` the
    accept pipeline.
  - **Evidence lives in the registry.** An accepted record is stored as an OCI 1.1
    referrer of the exact contract digest it is about, so the registry that already
    holds the contract is the only durable evidence system. No bucket, no database,
    no second persistence path.
  - **Contract catalog.** `pkg/catalog` answers what a set of contract roots and
    their closure contain, bounded and free of any delivery mechanism. It reaches
    agents over MCP as exactly two fixed read-only resources, `pacto://catalog` and
    `pacto://catalog/closure`, plus one tool, `pacto_catalog_revision` — and no
    resource templates, because a revision identity is four structured fields and a
    URI template would force the ad hoc encoding that identity discipline exists to
    prevent. The session is frozen, so a catalog answer cannot change underneath a
    conversation.
  - **Change impact.** `pkg/impact` and `pacto impact` answer who is affected by a
    change, computed over canonical identities and refusing a mutable reference.
  - **Reconciliation and observation.** `pkg/reconcile` compares the declared graph
    with the observed one; `pkg/otelobserver` reads an OpenTelemetry span export to
    discover calls nobody declared.
  - **CLI.** New `pacto fleet` (with `fleet reconcile`), `pacto evidence`,
    `pacto impact` and `pacto otel` command groups, all backed by the same read
    model.
  - **Dashboard.** A product-shaped interface over the graph: services, revisions,
    targets, owners and sources as first-class pages with canonical links between
    them, an attention view that ranks what is actually wrong, and a graph view
    that stays readable at fleet size. The wire contract is generated from OpenAPI
    end to end, so the frontend cannot invent semantics the backend does not have.
  
  Everything reports what it does not know. Evidence that is absent, stale, partial
  or unreadable is reported as such and is never rendered as a passing result.
  
  Backwards compatible: no existing flag, API or JSON shape changes.

## 3.1.4

### Patch Changes

- e5f696f: Fix the docs version selector so it opens on click. After the previous fix it no
  longer opened on hover (intended) but a `:focus-within` rule out-ranked the open
  class on click, so the dropdown stayed collapsed. Gated the hover/focus suppress
  rules with `:not(.md-version--open)` and raised the open rule's specificity.
  Docs-only; this core patch is the release that redeploys pacto.run/latest.

## 3.1.3

### Patch Changes

- f8aef8f: Deploy the docs version-selector fix to the live site: the mike version dropdown
  now opens on click, not hover, so it no longer pops over the nav tabs and swallows
  their clicks. Docs-only change (PR #286); no functional change to the engine, CLI,
  or dashboard. This core patch is the release that redeploys pacto.run/latest.

## 3.1.2

### Patch Changes

- bbc7b9c: Rebuild the operator and dashboard container images through the new native per-arch
  build pipeline: each architecture builds on its own runner (no QEMU emulation) and is
  merged into the multi-arch manifest. No functional change to the engine, operator, or
  dashboard — this release ships and validates the faster image pipeline.

## 3.1.1

### Patch Changes

- dd4dab1: Repo-wide audit remediation (engine, CLI, dashboard). Closes the OpenAPI
  breaking-change diff false-negatives — path-item-level parameters are now diffed,
  the request body is deep-diffed so a newly required property is BREAKING, and
  optional→required / added-required parameters are BREAKING — so a BREAKING-only
  release gate can no longer be bypassed. Further security and dashboard fixes land
  in the same PR.

  As the first release since v3.1.0, this also ships the previously-merged but
  unreleased dashboard **Ctrl+C shutdown fix** and the demo version-label fix.

## 3.1.0

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

## 3.0.1

### Patch Changes

- d09f2cc: Publish the demo bundles to monorepo-owned OCI coordinates.

  The demo bundles previously targeted `ghcr.io/trianalab/pacto-demo/*`, packages
  owned by the old `pacto-demo` repository that the monorepo cannot write. They now
  publish to `ghcr.io/trianalab/pacto/*`, created and owned by this repo. This also
  re-cuts the dashboard contract bundle (its publisher now installs the pacto CLI +
  plugins) and folds in the cel-go 0.29.0 bump. The core fixed group advances one
  patch (core, cli, dashboard-image, demo-bundles, dashboard-contract-bundle).

## 3.0.0

### Major Changes

- 045f11e: Pacto 2.0 — breaking contract-model, engine and module-path changes.

  - The Go module path becomes `github.com/trianalab/pacto/v3` (was `.../v2`).
    Consumers must update their import paths.
  - The contract schema is v2 only (`pactoVersion "2.0"`); v1 fields
    (`runtime.*`, interface `port`, `scaling`, `service.image`) are removed.
  - New pure engine: `pkg/evidence` + `pkg/finding` + `Evaluate(contract,
evidence)`; `ValidateRuntime` and the v1 declaration-side runtime types are
    gone.
  - Releasing is driven by an explicit release transaction, not a manifest-file
    diff.
