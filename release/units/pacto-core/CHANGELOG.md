# @pacto/core

## 3.2.7

### Patch Changes

- 5046822: Make the dashboard move, and make what it draws honest.
  
  Motion is a closed system rather than a per-component decision. Four roles —
  feedback, flip, reveal, dismiss — are declared once in `tokens.css`, and a
  ration governs who may use them: only an error state may enter on its own and
  carry the alarm ring, a warning may enter but never rings, and every other tone
  stays still. The number of moving things on a screen is the number of things
  wrong with it. Every entrance respects `prefers-reduced-motion`.
  
  The graph stops being a picture and becomes an instrument. The subject of the
  page carries a standing halo, so arriving on a graph tells you what it is about;
  a click pins a directional spotlight and re-frames the camera without ever
  re-laying-out; edges answer the pointer; a fit can no longer zoom past the point
  where labels are text. The legend was a caption listing distinctions the canvas
  actually draws — every entry is now a toggle that dims exactly that distinction,
  because a dense neighborhood is read by taking things out of it. Picking from
  the accessible text list points the canvas at the same node, so the two halves
  of the screen can no longer describe different things, and a summary line states
  the counts, what the legend is hiding and what is selected.
  
  Three bespoke charts are gone. A treemap, a donut and a second bar chart are
  replaced by the two house forms already used elsewhere, and
  `cytoscape-expand-collapse` — a dependency behind a toolbar nothing rendered —
  is removed with it.
  
  Chart corrections, each a case of the drawing contradicting the data:
  
  - The priority quadrant measured every service against a fixed midpoint while
    the gate is per service, so a service scoring 60 against a threshold of 80 sat
    on the healthy side of the line while failing. Points are now plotted as
    distance from their own threshold, and the divider says what it is.
  - Dot radius encoded blast radius, which is already the y position — the same
    number drawn twice. One radius for every dot.
  - The version timeline positioned markers by date and drew no date axis, so the
    only way to read one was to hover it.
  - Compliance status was three separate tables — the row badge, the legend swatch
    and the graph node — and they disagreed. "Unknown" was a blue badge, an amber
    swatch and a grey node on one screen. One table now decides both the wording
    and the tone, and every surface reads it.
  - Distribution shares printed a decimal below a population of a hundred, stating
    "12.5%" where the smallest step the data can take is 12.5 points. The same
    rounding ran out at the other end: one invalid target in a fleet of three
    thousand printed as "1 (0% of 3000)", a row contradicting the count beside it,
    and it is exactly the row a triage page exists to surface. A non-zero share now
    prints as the bound it is under rather than as nought.
  
  A drawer opening beside the graph pushed the page off the right of the screen.
  Cytoscape writes its current pixel width onto a wrapper inside the canvas, so the
  graph's minimum width was whatever it was last laid out at, and no track holding
  it could shrink. The graph now contains its own inline axis and sizes from the
  outside in, which fixes every layout that embeds it rather than the one that
  happened to notice. A claim's source revision is abbreviated rather than printed
  as a full digest, and the responsive gate reads the body's scroll width as well
  as the document's — clipped overflow is invisible to the document — at desktop
  widths as well, where the drawer that started this actually opens.
  
  Change analysis joins the long pages on the shared "On this page" rail. A finished
  analysis runs several screens deep — the revision pickers, the change table, then
  the consumer table under it — and getting back up to compare a different pair was a
  scroll. It is the navigator the overview and the entity pages already use, so there
  is no second contents list, and it lists only what the page actually rendered: there
  is no "What it affects" entry until there is a result.
  
  The overview draws populations it can count. Nine operational targets on a
  proportional bar is a shape the reader has to convert back into nine things;
  drawn as nine marks, two of them red, the count is the picture. Past a hundred
  and twenty members the marks stop being countable and the proportion is the
  honest reading again, so the bar comes back — and a population that over-counts
  itself is never drawn as marks at all, because it has no individuals to draw. The
  prose around them is cut: a sentence under the posture bars restated two bucket
  values that were already on the screen, so there are no longer two copies of the
  same number to check against each other.
  
  Those marks are now sized from the population, so a small one is big: nine
  targets drawn as nine fixed sixteen-pixel squares was a smudge in a
  four-hundred-pixel column — countable in principle and nothing to look at — and
  the size steps down only as fast as it has to for the row to keep fitting. The
  gutter and the corner are fractions of the mark, so a field of nine and a field
  of ninety are the same drawing at two scales rather than two different charts.
  The three posture questions sit in three columns where there is room, instead of
  two and an orphan below an empty half-row. And every distribution now puts its
  picture first: the description used to sit between the heading and the graphic,
  so a band of three charts was read as six lines of caveat with drawings between
  them, and each figure announced itself to a screen reader by reciting the whole
  paragraph. The prose is a footnote to the drawing, so it is printed under it.
  
  The WebAssembly demo's notice can be dismissed. It floats over the bottom of the
  dashboard and never left, which on a short window is where the content is. A
  failed engine load brings it back: that is the one message the reader cannot be
  allowed to have closed.
  
  Ten scale limits are closed. Measured against a fleet of five hundred services,
  two thousand revisions, three thousand targets and eight thousand relationships,
  each of these was a place where the cost of an answer grew with the size of the
  fleet rather than with the size of the question:
  
  - Every concurrent request that missed a cold service index ran its own full
    serial resolution of the whole fleet. One rebuild is admitted at a time and the
    callers queued behind it return the index it stored, doing no work at all.
  - A Fleet host issued a whole-fleet `/api/services` it never reads, every two
    seconds. The capability probe used to race the load it decides the shape of, so
    the first pass always read "capabilities unknown", took the legacy branch and
    paid for an answer it threw away. It is probed first now, at the cost of one
    round trip on first load. The poll also no longer stacks a second pass on top
    of one still in flight, while a manual refresh is still never dropped.
  - The whole-fleet dependency graph had no node bound. It takes one, defaulting to
    the engine's ceiling, and every node carries the path taken to reach it — so an
    unbounded deep answer was quadratic on the wire, not linear.
  - `Meta` applied none of the envelope caps `ProductMeta` applies, so two answers
    from the same snapshot could disagree about how much they left out.
  - The per-target revision match rebuilt its candidate set from every revision in
    the fleet; it is grouped once per service and reused.
  - The bulk snapshot export round-tripped through a marshal-and-unmarshal
    defensive copy on its way to a wire it is then written to and dropped.
  - A truncated graph said only that it had been truncated. It now says how much is
    missing, and offers the next node budget — sixty, a hundred and fifty, five
    hundred, the same rungs the backend will honour, because a control that offers a
    step the server then clamps is a control that lies about what it just fetched.
    The budget is part of the URL, so a shared link reopens the graph being
    discussed rather than a smaller one.
  - A fit clamped at the legibility floor left part of the graph off screen and
    said nothing, and a cropped canvas looks exactly like a complete one. It says so.

## 3.2.6

### Patch Changes

- e36b181: Reframe the README, the pacto.run homepage and the pages that define what Pacto
  is around a positive category noun: **operational contract system**.
  
  The definitional slot on both front doors was held by an analogy
  ("Pacto is to service operations what OpenAPI is to HTTP APIs") and, on the
  homepage, by an eyebrow reading "Open contract standard". The first installed
  "file format" as the category and discarded the engine; the second promised
  governance and a second implementation that do not exist. Both are replaced by
  a definition that states what a contract records, how it is published and what
  it is compared against.
  
  The category noun says what Pacto is; one sentence beside it now says what that
  is for. Pacto gives software a machine-readable operational interface — a
  versioned description of what a service is, what it exposes, what it depends on
  and what it promises — so platforms, CI systems, controllers, automation and
  agents consume the same interface instead of each reconstructing operational
  knowledge from deployment files, documentation and runtime state.
  
  Structural changes:
  
  - `README.md` leads with the category, then the problem (operational facts with
    nowhere to live, and a dependency edge that carries no version range), then
    the mechanism, then who reads a contract, then why software operating software
    raises the price of not having one. "What Pacto is NOT" drops from four
    bullets to three and moves behind all of it.
  - `docs/index.md` moves "What Pacto is not" below "The problem" — non-goals
    disambiguate a model the reader already holds and cannot build one. The
    heading and its anchor are unchanged, so `#what-pacto-is-not` still resolves.
  - The hero's only jump link now points at `#what-is-pacto` rather than at the
    list of exclusions.
  - The IDP contrast is cut back to a single non-goal bullet. What replaces it as
    the differentiator is version shape: a catalog entry records that an edge
    exists, a Pacto dependency records the range it accepts and pins the closure
    by digest.
  
  Corrections found while verifying the copy against the implementation:
  
  - `Diff · Graph · Enforce · Verify` becomes `Diff · Graph · Validate · Verify`.
    There is no `pacto enforce`; policy is Layer 3 inside `validate`, and
    `MANIFEST.md` disowned "enforcement" eight lines below the slogan.
  - `docs/contract-reference/sections.md` claimed a `configurations[].ref` is
    resolved from the referenced bundle at the fixed path
    `configuration/schema.json`. No code reads that path. The reference is
    validated as well-formed, recorded as a reference edge and pinned in
    `pacto.lock`; the recursive-resolution claim is scoped to the lockfile, which
    is the surface that actually walks the closure.
  - `docs/index.md` said `pacto generate` produces deployment artifacts. It
    invokes a `pacto-plugin-<name>` binary you supply; Pacto ships no generators.
  - "Blast-radius analysis" as an MCP capability becomes impact analysis, the
    feature that exists.
  - The Kubernetes overview stated "never modifies" and then retracted it. It now
    leads with the actual grant — `get`, `list`, `watch` on watched workloads —
    and keeps the managed-component escalation as the second half of the same
    paragraph rather than as a retraction.

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
