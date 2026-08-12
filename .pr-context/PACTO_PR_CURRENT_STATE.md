# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-12  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`6750c95921e60969d54859a10d8f8c287eefb58c`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

That review NARROWLY REOPENED Phase 8 on two counterexamples (blockers A and B
in section 2) and left every other Phase-8 acceptance frozen.

Commits appended on top of the reviewed HEAD `6750c959`, oldest first:

- `d8ef5d5a` — one published artifact is one revision, whatever found it
  (blocker B)
- `0cf0c69b` — a round of facts is only a fact if one snapshot answered all of it
  (blocker A)
- this document's own commit — persist the Phase-8 state after blockers A and B

For completeness, the full appended range since the reviewed HEAD `5f3d4ebb`:

- `d098044b` — persist the independently reviewed PR state at `5f3d4ebb`
- `8b0f26b0` — a data source is exactly the one thing it declares itself to be
- `fa76b69a` — every value the chart accepts survives the trip to the dashboard
- `ae29a13d` — the source boundary is written down, and Kind proves it
- `eedab3f7` — the escaping source gets its own claim
- `4ba54a13` — persist the independently reviewed PR state at `eedab3f7`
- `b3075ece` — a refused identity is not a process that failed to start
- `de13fb2a` — the roadmap names the test-architecture debt before it grows
- `1a79ec32` — the fleet sees the contract revisions the cluster resolved
- `59632e22` — a controlled plain-HTTP registry is reachable by what the operator
  manages
- `0c67164d` — the newest published revision is content the product can analyze
- `f0cf50a6` — the live vertical publishes the revisions the product must reason
  about
- `731b9692` — the browser proves the product, not that a page rendered
- `a6755934` — the insecure-registry list iterates without materializing a slice
- `cfeebce8` — the generated helm reference names the insecure-registry value
- `3405d762` — a declared interface needs something to be observed against
- `2a622eb3` — the reference provider sizes its buffers without arithmetic
- `9bdab0b0` — a plain-HTTP registry answers which versions it holds
- `b5be4f4e` — a disclosure has no accessible name, so the test id is its handle
- `b8424175` — rebuild the UI bundle (generated)
- `7ffdf884` — an exact revision match over a scheme-less pin is not retrievable
  content
- `2c5034d8` — persist the Phase 8 candidate state and what it does not close
- `d18ca70e` — a port-forward is ready when it answers, not two seconds later
- `6750c959` — the Phase-8 candidate is verified on the fixed harness
- `d8ef5d5a`, `0cf0c69b` and this document's commit, as listed above

This section records the last INDEPENDENTLY REVIEWED state. It is not a Claude
self-assessment and must not be re-closed by the session that implements against
it.

### Handoff discipline — still in force

Every handoff MUST enumerate EVERY commit between the reviewed starting SHA and
the final SHA of the iteration, including coordination-state and
generated-bundle commits. A commit that only touches `.pr-context/` is still a
commit an independent reviewer has to account for.

### Accepted at this review — do NOT reopen

The following are settled. Do not redesign them, and do not "improve" them as a
side effect of the current phase. Reopening any of them requires a NEW concrete
counterexample.

**Ownership identity and discovery**

- `OwnerKey` and the exact-versus-fuzzy owner semantics;
- contacts-only ownership without a fabricated canonical identity;
- the shared `OwnershipFact` presentation across Service, Revision and
  Operational Target detail;
- Revision contact inspection;
- contact-order and exact-duplicate normalization (`Owner.ContactSet`);
- owner ranking namespace ambiguity;
- the ownership aggregate semantics.

**Distribution primitive**

- `DistributionBar` under-count behaviour;
- `DistributionBar` ordinary over-count behaviour;
- `DistributionBar` authoritative-zero behaviour.

**Product surfaces**

- the Operational Overview dashboard generally;
- Data Sources as a secondary Product surface (four primary tabs unchanged);
- the Data Sources Overview section and its `PageToc` integration;
- source chips as exact links;
- the Data Sources inventory;
- selected-source health versus Fleet knowledge as distinct concepts;
- the Source-detail Product hierarchy;
- raw source records versus contributed Product entities;
- `SourceContribution` complete-population semantics;
- `PageToc`;
- Services aggregates;
- Readiness;
- typography;
- progressive disclosure;
- Graph / Change-analysis geometry.

**Model**

- lock v3.

**Knowledge population**

- `ProductMeta.SourceCounts` is the authoritative complete population for
  source-health arithmetic; `ProductMeta.Sources` is a bounded named preview.
  The Product renders one answer for one snapshot, at every level (Data Sources
  tally, `KnowledgeBanner`, knowledge severity).

**Phases**

- Phase 1 through Phase 7 are closed.

## 2. Current phase status

### Phase 1 — COMPLETE

### Phase 2 — COMPLETE

### Phase 3 — COMPLETE

### Phase 4 — COMPLETE

### Phase 5 — COMPLETE

### Phase 6 — COMPLETE

### Phase 7 — COMPLETE

Target delivered:

**operator-managed OFFLINE observation/trace-source configuration**

The offline pipeline (OTLP/JSON trace file, offline `pkg/otelobserver`, observed
edges, Fleet observation source, reconciliation) pre-existed and is accepted.
Phase 7 added the declarative, operator-managed way to package, configure and
mount those observation sources, with stable Data Source identity that does not
depend on list position. Phase 7 was NOT an OTLP receiver, a Collector, a trace
database or any live ingestion.

Everything about Phase 7 is closed and must not be redesigned without a new
counterexample: the offline analyzer boundary, the operator-managed
observation-source concept, PVC + ConfigMap as the only backings, read-only
mounts, no `hostPath`, deterministic sorting, reordering changing neither
identity nor pod template, complete removal, source failure as explicit
Fleet/Product knowledge, source health separate from observation freshness, the
retained ad-hoc `pacto dashboard --traces`, the focused Kind
observation-packaging scenario, and the existing architecture gates.

#### Blockers A, B and C — independently CLOSED at `eedab3f7`

The three defects that reopened Phase 7 at `5f3d4ebb` were independently
reviewed at `eedab3f7` and are accepted. They must NOT be reopened, and the
implementations behind them must NOT be redesigned:

- **A — whole-Fleet Data Source identity uniqueness.** `checkSourceIDsAreUnique`
  over the FINAL assembled source set, failing closed in `Service.Fleet` before
  `fleet.Build`, so no ambiguous Product Data Source key is ever published.
- **B — rooted observation-file reads / symlink escape prevention.**
  `os.OpenRoot` / `Root.ReadFile` against the declared source root, the
  single-segment managed file contract, projected-ConfigMap internal symlinks
  still working, no Kubernetes dependency in the offline parser.
- **C — Helm values -> operator flag -> parser configuration-wire fidelity.**
  The restricted lexical space, `ParseObservationSource`, the backing-name
  validation, and the Helm-rendering test that parses the ACTUAL rendered
  argument.

#### Final Phase-7 blocker — CLOSED at `b3075ece`

The public documentation claimed that on a Data Source identity collision
"Pacto refuses to start". That was never the implemented behavior, and the
runtime behavior was correctly NOT changed to make the prose true. The prose was
corrected to the implemented and accepted lifecycle:

- `Service.Fleet` detects duplicate source ids and returns an error BEFORE
  `fleet.Build`, so no ambiguous `FleetSnapshot` is ever published;
- `fleet.Manager.Start` performs `Refresh`, and a refresh failure is not
  process-fatal: the last good snapshot is retained when one exists, there is no
  snapshot when the first refresh has never succeeded, and the manager keeps
  running and can retry;
- the dashboard HTTP host itself stays alive.

"The process failed to start" is not equivalent to "an ambiguous Product identity
was refused publication". The second is the invariant Phase 7 requires and
implements, and it is what the documentation now says.

#### Accepted scoped deviation — carried into Phase 8

The live Kind observation scenario asserts the observed edge under its declared
identity and that it names the same pair the operator reconciled as declared,
but not the snapshot's `reconciliation: "matched"` verdict, which needs a
contract-REVISION source the operator-managed dashboard does not have in that
scenario (the live Kubernetes source projects deployed targets, not revisions).
That verdict over an observation source stays proven hermetically in
`internal/app` and by `make demo-fleet`. The fully live declared+observed
Product reconciliation is **Phase 8 work**.

### Phase 8 — CANDIDATE, NOT independently reviewed

Scope as commissioned: canonical LIVE Kind PRODUCT acceptance. Upgrade the
EXISTING live Kind vertical from a deliberate browser SMOKE check into
representative live Product acceptance: real OCI contract revisions published to
the in-cluster registry, digest-pinned operator resolution, a managed observation
source in the SAME operator-managed dashboard, live declared+observed
reconciliation reaching `matched` against the real Product API, real Change
analysis over two canonical revisions, and the existing external signed-evidence
target preserved.

Not another Kind vertical. Not a test-architecture refactor. Not Phase 8B.

Implemented at `d18ca70e`, NARROWLY REOPENED by the independent review at
`6750c959` on two counterexamples, and re-implemented at `0cf0c69b`. This is a
Claude self-report and closes nothing: the phase is a CANDIDATE until an
independent review says otherwise.

#### Narrow reopen at `6750c959` — blockers A and B, closed at `0cf0c69b`

Everything else accepted in Phase 8 was FROZEN for this pass and is unchanged:
the real checkout A/B, orders and payments bundles; the digest-pinned Pacto CRs;
per-refresh Kubernetes-to-OCI discovery; the controlled insecure-registry
plumbing; the real checkout interface binding; the named managed observation
source; backend-authoritative matched reconciliation; the real checkout A-to-B
Change analysis and orders impact; the external Evidence Server target; the
Go/Playwright responsibility split; journeys A-H; and the shared port-forward
readiness fix.

**Blocker A — `productready` did not prove ONE coherent snapshot.** The prober
made a dozen Product requests, discarded every response's `Meta.SnapshotID` and
read an Overview id only after the semantic checks were already done. A Manager
refresh between any two requests spliced facts from different snapshots, the
gate passed on a fleet that never existed, and `PW_FIXTURE.snapshotId` named a
snapshot that had proved nothing.

Closed at `0cf0c69b`. A round now ADOPTS the id of its first response, and every
later response in that round must repeat it; a different id, or no id at all,
discards the whole round and retries, and a discarded round cannot emit fixture
keys. Snapshot coherence is fact 13 of 13. The invariant is pinned by adversarial
Go tests over a controlled Product server in
`tests/e2e/kind/productready/main_test.go`: one coherent id passes; a changed id
on a list, on a detail, and on the neighborhood each fail; a response naming no
snapshot fails; a first-response outlier fails; a spliced round fails even when
every individual fact holds; and a failed mixed round emits no keys. No sleep and
no timing assumption is part of the invariant.

**Blocker B — `IncludeCache` plus OCI published two identities for one real
revision.** Once a registry pull had populated the pod cache, `CacheSource`
reconstructed a reference from the cache PATH — and `cachePath` maps every `:`
to `/`, so the path cannot say where a registry host ends, nor whether `:1.0.0`
is a tag. The reconstruction invented a domain and carried no manifest digest,
so `fleet.revisionFrom` keyed it by a derived content digest while `OCISource`
supplied the SAME published artifact under its manifest digest: two
`RevisionKey`s, the second an unresolved shadow.

Closed at `0cf0c69b`. Identity is RECORDED at pull time in a `ref.json` sidecar
beside the bundle (`oci.CachedRef`), so the cache reader agrees with the registry
instead of guessing from a filename, and both sources emit one `RevisionKey`
whose `Sources` union names both. The sidecar is written BEFORE `bundle.tar.gz`,
because a cache walker keys on the bundle: an artifact can never be visible
before the record of what it is. Offline stays offline — the digest is read from
disk, and a disconnected build makes zero registry calls. Nothing is collapsed by
service and version: two genuinely different immutable digests declaring the same
version remain two revisions, because that is a re-published tag, not a
collision. Pinned by production-level tests in
`tests/e2e/fleet_cache_identity_test.go` (real registry, real `pacto push`, real
`CachedStore`, real `app.Service.Fleet`): a cold build plants no shadow, a warm
cache-plus-OCI build yields exactly one canonical revision per artifact with both
sources and an `oci://...@sha256:` pinned ref, two distinct digests at one version
stay two revisions, and a cache-only build is available, network-free and still
digest-exact.

The gate enforces the same invariant live: each fixture service/version must
resolve to EXACTLY ONE revision, which must then be `exact` and retrievable. Two
revisions fail the round; the gate never picks the first retrievable match.

The Kind vertical now runs `productready -snapshots 2`, so the browser layer only
starts after TWO DISTINCT snapshots have each proved all thirteen facts. The pod's
OCI cache starts empty and the first refresh's pulls are what fill it, so only a
later snapshot has the registry and the now-populated cache contributing the same
artifacts — the post-cache state blocker B lives in. It is reached by observing
it, not by sleeping. A pod restart would be the wrong lever: the operator mounts
the dashboard cache as an `emptyDir`, so restarting ERASES the state under test.

One CI-reachability defect was fixed with them: the gate is a Go program under
`/tests/`, which the coverage leg excludes and the e2e leg did not match, so its
tests never ran anywhere. `make e2e` runs them now.

What the live cluster now proves, in the cluster and in the browser:

- four bundles published to the in-cluster registry through the real `pacto push`
  (payments, checkout 1.0.0, checkout 1.1.0, orders), each resolved to an
  immutable manifest digest;
- both Pacto CRs resolve `contractRef.oci` at a digest, so the operator's
  `status.contract.resolvedRef` is a real resolved contract identity and the
  dashboard reaches the SAME content back through the registry;
- `tests/e2e/kind/productready` (Go) gates the browser layer on thirteen facts
  re-checked every round against the live Product API — and, since `0cf0c69b`,
  on TWO distinct snapshots each proving all of them — and emits the keys it
  DISCOVERED as `PW_FIXTURE`, so no browser journey constructs an identity;
- eight live Product journeys (A–H) in `pkg/dashboard/frontend/e2e-live/`, all
  passing on the Kind `operational-graph` shard at `d18ca70e` and again at
  `0cf0c69b`, where they run only after the post-cache snapshot.

Shell classification, as section 12 of the commission requires. Every shell
change in Phase 8 is thin orchestration; none of it parses JSON, decides
ontology or owns retry semantics — those live in `tests/e2e/kind/productready`
(Go) and in the Playwright specs:

- the fixture bring-up added to `tests/e2e/kind/operational-graph.sh` (four real
  `pacto push` invocations, two Pacto CRs, one ConfigMap, Helm values) is a
  sequence of real CLI calls;
- `pf` moved to `tests/e2e/kind/lib.sh` at `d18ca70e` and now waits for the
  forward to answer instead of sleeping two seconds. This is a bug fix, not the
  Phase 8B consolidation: `pf` was the one helper duplicated verbatim in three
  scripts AND the direct cause of a shard failure, so it was fixed at the root
  rather than patched in the one script that happened to fail. The rest of the
  duplicated lifecycle named in TARGET section 10 — cluster setup, Helm
  invocation, eventually-helpers, cleanup, diagnostics — is UNTOUCHED and remains
  Phase 8B's inventory to do properly.

Disclosed, NOT fixed, and NOT in Phase 8 scope — for independent triage:

- every Kubernetes target's Content badge reads `Local reference (not
  retrievable)` even when the operator pinned a digest, because the operator
  writes `status.contract.resolvedRef` WITHOUT the `oci://` scheme and
  `classifyOCIRef` will not read a scheme-less string as a canonical ref. The
  revision-match dimension is unaffected (`exact`), the pairing is documented in
  `pkg/fleet/ref.go` and pinned by `TestTargetIdentity_ExactMatch_NonRetrievable`
  with that exact operator shape, and journey D now asserts the true live state.
  Whether the operator SHOULD emit a canonical `oci://` ref is a product question,
  not a Phase-8 change.
- `pkg/dashboard/frontend/e2e-live/` is NOT type-checked: `tsconfig.json`
  `include` is `src/**`, so `npm run lint` never sees the live specs. A type error
  there surfaces only when Playwright transpiles it inside a Kind shard.
- `helm-docs-check` rewrites `charts/pacto-dev-gateway/README.md` as a side
  effect of running; the file must be restored before committing.

The fourth disclosure carried at `d18ca70e` — `IncludeCache` plus the OCI source
minting a second `RevisionKey` for the same content — became blocker B of the
narrow reopen and is closed above. The shell classification is unchanged by this
pass: `tests/e2e/kind/operational-graph.sh` gained one flag and a comment, still
thin orchestration, and no new harness was created.

### Phase 8B — NOT STARTED

Test architecture & harness consolidation. See TARGET section 10. Phase 8B MUST
close before Phase 9 or Phase 10 add their new acceptance harnesses.

### Phase 9 — NOT STARTED

Real built MkDocs browser E2E.

### Phase 10 — NOT STARTED

Docker Desktop/containerd/local-registry Kind path.

### Phase 11 — NOT STARTED

MCP catalog core: bounded multi-root catalog semantics over arbitrary Pacto
contract roots. Distinct from the operational Fleet MCP tools this branch
already ships.

### Phase 12 — NOT STARTED

MCP catalog discovery server/CLI/E2E/docs.

### Phase 13 — NOT STARTED

Normative invariants finalization.

### Phase 14 — NOT STARTED

Finalization, final ontology audit, repository hygiene, PR body, exact SHA, readiness.

## 3. Branch-hygiene regression — CLOSED

The four local-agent tooling files (`.claude/CLAUDE.md`, `.codex/config.toml`,
`.mcp.json`, `AGENTS.md`) were untracked by the append-only commit `ef67f494`
and remain locally as untracked tooling. The incidental `go.work.sum` entries
were reverted by `fffd9a39`, and the 47MB tracked build output by `ccf2ae0e`.
No history was rewritten.

The rule stands for every later phase: local/agent tooling, caches, plans and
workstation paths must never become tracked files of this PR.

## 4. Accepted Product architecture

Do not reopen these decisions without a new concrete counterexample.

### Canonical entities

- Service
- Contract Revision
- Operational Target

They remain distinct.

### User-facing terminology

- `target` internal kind -> **Operational target**
- `source` user-facing -> **Data source**
- Collector remains a distinct narrower evidence-producer concept
- user-facing "Fleet" jargon removed where it assumes internal knowledge
- internal `pkg/fleet`, `/fleet/...` and CLI/internal identifiers may remain

### Primary Product navigation

- Overview
- Services
- Operational graph
- Change analysis

Secondary/contextual:

- Needs attention
- Owners
- Data sources
- Readiness through revision/attention context

### Readiness

Readiness is per Contract Revision.

Fleet-host standalone legacy Readiness is retired/canonicalized.

### Change analysis

Legacy Compare + Product Impact were consolidated.

One canonical Product workspace answers what changed and what it affects. Detailed semantic field diff is preserved.

### Product entity responsibility

**Service**
- logical identity + aggregate operational posture.

**Revision**
- contract inspector.

**Operational Target**
- runtime/evidence inspector.

**Data source**
- provenance inspector: what this source is, whether it is healthy and current,
  what it supplied and what is attributable to it.

## 5. Accepted information-parity work

Earlier Product migration accidentally lost significant V1 inspection capability.

Subsequent work restored the intended model.

### Revision inspector

Supported information is reachable for:

- interfaces;
- operations;
- configurations;
- policies;
- capabilities/tools;
- skills;
- dependencies;
- readiness;
- docs;
- SBOM/software inventory;
- validation;
- identity/provenance;
- declared owner contact metadata;
- revision history;
- Change analysis entry.

### Markdown

Product Revision can again read bundled Markdown.

Current accepted architecture:

- docs listed in Revision detail;
- body fetched lazily;
- canonical RevisionKey + path boundary;
- bounded resource read;
- Markdown rendered safely;
- Mermaid rendered;
- path traversal rejected;
- same-name different-domain isolation.

### Immutable document correction

- Build records SHA-256 fingerprint for listed document content;
- lazy read verifies bytes against fingerprint;
- changed/deleted/unreadable body becomes explicit unavailable;
- 512 KiB bound remains;
- source conflicts do not silently pick arbitrary bytes.

## 6. Accepted UI/interaction corrections

- Services search autocomplete: debounced, bounded, canonical identity,
  same-name domain disambiguation, keyboard/pointer, stale-response protection.
- Same-query stale-while-revalidate: first load / changed identity may load;
  same-query refresh retains last valid data; refresh failure retains stale data
  with an honest notice; a changed query never presents old rows as the answer.
- Scroll restoration: scroll belongs to a history entry, not only a URL; push
  starts fresh; Back/Forward restores the exact entry; hard reload restores the
  current entry; canonical replace preserves the entry; state is bounded; user
  scroll input can cancel pending restoration.
- Graph spatial state: same-query refresh preserves layout; reload restores
  spatial state; query identities isolate state; semantic data still refreshes;
  `Fit` and `Reset layout` are different.

## 7. Accepted visualization direction

Accepted principle:

> lists/tables for exact inspection; visualizations for system comprehension.

Any chart must answer a real question, use authoritative aggregate data, not
infer global truth from a page, preserve exact values, expose
uncertainty/completeness and remain accessible/mobile/light/dark.

## 8. Latest verification snapshot

Reviewed at exact HEAD `6750c959`. That review reopened Phase 8 narrowly; see
section 2.

### Post-reopen verification — self-reported at `0cf0c69b`

Not an independent review. Re-verify at the exact SHA before accepting it.

- GitHub CI run `31625253138` at `0cf0c69b`: every job green, including
  `ci-gates`, `ci-static`, `ci-engine`, `ci-oci`, `ci-dashboard`,
  `ci-e2e-envtest`, `ci-integration-kubernetes`, `dashboard-e2e`,
  `operator-build`, `artifact-drift`, `release-version-test`,
  `release-dry-run`, and all six Kind shards — `reconcile`, `dashboard`,
  `upgrade`, `evidence`, `observation` and `operational-graph`.
- `ci-e2e-envtest`, `ci-integration-kubernetes` and the `bundle` job of Pacto
  Contract CI (run `31625253128`) each failed on the FIRST attempt with a
  `503 Service Unavailable` from `github.com/.../releases` while downloading a
  third-party binary (envtest 1.36.2, helm-unittest, syft 1.42.3). None of the
  three touches the changed packages. `gh run rerun --failed` on both runs made
  them green with no code change; both runs are green at `0cf0c69b`.
- Other workflows at `0cf0c69b`: Security, Docs check, Repowise, Validate PR
  title and Pacto Contract CI all green. `CodeQL` reports fail — the
  carried-forward item below, with two new alerts from this pass, disclosed
  there.
- The `operational-graph` shard (job `94209933398`) shows the post-cache state
  reached by observation: snapshot `sha256:24bad63f...` proved 13 facts on round
  4, then snapshot `sha256:2ef5f486...` proved all 13 again on round 10 — a
  second, distinct snapshot after a dashboard refresh, whose facts include
  exactly one canonical, exact, retrievable revision for each of checkout 1.0.0,
  checkout 1.1.0 and orders 1.0.0 with the cache already populated. The eight
  live Chromium journeys (A–H) then passed against that state.
- Locally at the same tree: `ci-static-engine` (fmt, vet, gocyclo, lint,
  check-section, CLI-docs drift, UI-build drift, dashboard-SDK drift) clean;
  `ci-test` 100.0% total coverage with the race detector; `ci-gates`;
  `make e2e` (the engine e2e suite, including the three new
  `fleet_cache_identity` production tests, plus the productready gate's own
  tests, which `make e2e` now runs at all); `make demo-fleet` (cluster-free
  operational-graph acceptance, all sections PASS); `ci-ui` — Vitest 1232 passed
  in 67 files.
- No authored frontend input changed in this pass, so the committed UI bundle
  was NOT rebuilt; `ci-ui-drift` and `check-dashboard-sdk-drift` are clean
  against the existing bundle.
- PR at `0cf0c69b`: open, DRAFT, mergeable. No rebase, no amend, no history
  rewrite, no force-push: `d8ef5d5a` and `0cf0c69b` are appends on top of
  `6750c959`, and this document's commit appends on top of them.
- Review threads re-queried at `0cf0c69b` (paginated, 192 threads): 184
  resolved, 8 unresolved, all CURRENT (none outdated). Six are
  `github-code-quality` comments on the GENERATED minified Mermaid chunk
  `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`, unchanged because
  the bundle was not rebuilt. Two are NEW `github-advanced-security` CodeQL
  comments on AUTHORED code — `pkg/oci/cache.go` lines 260 and 261, the sidecar
  write added by blocker B's fix — and are recorded in the carried CodeQL item
  below. So: 2 unresolved authored, 6 unresolved generated.

### Phase-8 candidate verification — self-reported at `d18ca70e`

Not an independent review. Re-verify at the exact SHA before accepting it.

- GitHub CI run `31612913647` at `d18ca70e`: every job green, including
  `ci-gates`, `ci-static`, `ci-engine`, `ci-oci`, `ci-dashboard`,
  `ci-e2e-envtest`, `ci-integration-kubernetes`, `dashboard-e2e`,
  `operator-build`, `artifact-drift`, `release-version-test`,
  `release-dry-run` and all six Kind shards (`dashboard`, `evidence`,
  `observation`, `operational-graph`, `reconcile`, `upgrade`).
- Security, Docs check, Pacto Contract CI, Repowise and Validate PR title: green
  at the same SHA. `CodeQL` reports fail — that is the carried-forward item
  below, unchanged.
- Locally at the same tree: `ci-static-engine` (fmt, vet, gocyclo, lint,
  check-section, CLI-docs drift, UI-build drift, dashboard-SDK drift),
  `ci-engine`, `ci-gates`, `ci-dashboard`; frontend `svelte-check` 0 errors /
  15 warnings across 799 files, Vitest 1232 passed in 67 files, offline WASM
  Playwright 219 passed.
- The committed UI bundle was rebuilt COLD via `make ui-build` and committed as
  `b8424175`; `ci-ui-drift` is clean at `d18ca70e` and the tree is unchanged by
  a rebuild.
- PR at `d18ca70e`: open, DRAFT, mergeable, no history rewrite, no force-push.
- Review threads re-queried at `d18ca70e` (paginated, 190 threads): 184
  resolved, 6 unresolved. All 6 are `github-code-quality` bot comments on the
  GENERATED minified Mermaid chunk
  `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`, all CURRENT (not
  outdated) because the bundle rebuild moved the path. 0 unresolved authored
  threads. Generated assets are not hand-edited.

One earlier candidate SHA, the docs-only `2c5034d8`, failed its
`operational-graph` shard. That was a harness defect, not a product one, and it
is the defect `d18ca70e` fixes: `pf` slept two seconds instead of waiting for the
port-forward, and the registry push piped its output into `grep`, so under
`set -o pipefail` the script died at a push that had nothing to connect to, with
the error text already eaten by the pipe and no `FAIL:` line printed at all. A
green run before that flake is not what this section reports; `d18ca70e` is a
full matrix on the fixed harness.

### Cross-cutting PRE-MERGE SECURITY item — OPEN, carried forward

Claude reports open CodeQL alerts on `refs/pull/291/head` rather than on `main`:
seven Go path-injection alerts plus one Python alert also present on `main`.

The exact inventory is **NOT** independently verified: the reviewing GitHub
integration cannot reach the code-scanning alerts API. The current source
inspection makes the reported false-positive explanation plausible, but plausible
is not resolved.

Therefore:

- do not describe these alerts as resolved;
- do not describe them as main-lineage;
- the Security workflow's own green status is a DIFFERENT claim from CodeQL alert
  attribution and does not close this;
- the alerts must remain visible and must be independently triaged, fixed or
  explicitly dismissed with evidence before Phase 14 readiness;
- do NOT make unrelated security-code changes in an intervening phase unless a
  NEW real counterexample proves the current explanation false.

This item is cross-cutting. It must not be pulled into a feature phase's scope,
and no feature phase must be blocked on it.

Re-verify both populations at the exact final SHA of every later pass; the
generated asset path changes whenever the UI bundle is rebuilt.

Generated Mermaid/minified assets must not be hand-edited. After the FINAL
generated bundle of a pass is committed, review threads must be queried again
and reported as unresolved authored / unresolved generated / current versus
outdated, with the exact current generated path.

The Security workflow's own status is a different claim from CodeQL alert
attribution. Do not describe CodeQL alert provenance as independently
established without inspecting the underlying alert records and their base/head
evidence.

Counts in a handoff are not evidence.

Re-queried at `d18ca70e` (still a Claude report, still NOT independently
verified, still OPEN): 8 open alerts on `refs/pull/291/head`, unchanged in
population — 7 `go/path-injection` (`internal/app/resolve.go` 35, 43, 57, 67;
`pkg/oci/cache.go` 230, 250, 254) and 1 `py/incomplete-url-substring-sanitization`
(`release/scripts/docs_check.py:197`). No security-code changes were made in
Phase 8; none of these are described as resolved, dismissed or main-lineage.

Re-queried again at `0cf0c69b` (same caveats, still OPEN): 10 open alerts on
`refs/pull/291/head` — 9 `go/path-injection` and the same 1 Python alert. The
population GREW BY TWO in this pass, and that growth is disclosed here rather
than folded into the existing item:

- alerts 40, 41, 42, 43 (`internal/app/resolve.go` 35, 43, 57, 67) — unchanged;
- alerts 45, 46, 47 (`pkg/oci/cache.go` 301, 321, 325) — the same three
  previously reported at 230, 250, 254; the lines moved because blocker B's fix
  inserted code above them, and no security code was changed;
- alerts 56, 57 (`pkg/oci/cache.go` 260, 261) — NEW, on the `MkdirAll` and
  `WriteFile` of the `ref.json` sidecar that blocker B's fix added;
- alert 38 (`release/scripts/docs_check.py:197`) — unchanged.

The two new alerts are the SAME family, behind the same barrier, as 45/46/47:
their path is `filepath.Dir(c.cachePath(ref))`, and `cachePath` already contains
the result to the cache directory with an explicit `filepath.Rel` plus `..`
check, returning a fixed `_invalid` path otherwise. CodeQL does not model that
barrier. That explanation is PLAUSIBLE, NOT VERIFIED, and it is exactly the
explanation this item refuses to accept without inspecting the alert records. So:
the two new alerts are OPEN, are not described as resolved, dismissed or
main-lineage, and are added to the population that must be independently triaged
before Phase 14 readiness. No attempt was made to silence them, and no unrelated
security code was touched.

Important process rule:

**Do not trust reported counts blindly in a later chat. Re-verify exact final SHA, CI and review threads before accepting the next handoff.**

## 9. Historical constraint

Authored-content U+00A7 gate is expected green.

Blocking enforcement over old branch commit history / PR metadata remains constrained because historical branch commit messages already contain the forbidden character.

No permission exists to rewrite shared history.

Do not rebase/filter-history/force-push to solve that unless Eduardo explicitly authorizes it.

## 10. Next iteration objective

**Phase 8 — canonical live Kind Product acceptance.** The branch already carries
a full live vertical (operator, dashboard, Evidence Server, in-cluster OCI
registry, reconciled Pacto CRs, external signed evidence, live HTTP Product API,
Playwright over Chromium). Phase 8 makes that EXISTING vertical rich enough that
a representative live Product journey has actual topology, revisions, targets,
observed evidence and reconciliation. Its detailed target is TARGET section 10,
"Phase 8 — live Kind Product acceptance breadth".

At `0cf0c69b` that vertical exists and the two counterexamples the review raised
against it are closed (section 2). The next iteration objective is therefore the
INDEPENDENT REVIEW of the Phase-8 candidate at its exact final SHA — not new
Phase-8 breadth, and not Phase 8B, which remains NOT STARTED.

Hard boundaries for the Phase-8 session:

1. do NOT create a new large `.sh` acceptance harness; extend the existing
   `tests/e2e/kind/operational-graph.sh` vertical and classify every shell
   addition as thin orchestration or explicitly deferred Phase-8B debt;
2. do NOT begin Phase 8B; its target is persisted in TARGET section 10 and must
   not be implemented in the Phase-8 pass;
3. do NOT add a fixture-only Product shortcut; the dashboard must discover OCI
   revisions through the actual operator status path;
4. do NOT re-derive reconciliation in Playwright; the backend value is
   authoritative and the browser proves consistent presentation;
5. do NOT erase the open CodeQL item in section 8;
6. Phases 1 through 7 must not be reopened or redesigned as a side effect.

## 11. Final-phase requirements already agreed

When the whole plan reaches the final phase, two deep audits are mandatory.

### Final ontology audit

Very thoroughly compare:

**intended ontology <-> code/types/behavior <-> docs/API/UI**

Must validate all first-class concepts, identities, relationships, epistemic states, absence semantics, provenance and aggregation semantics.

Do not declare merge-ready if ambiguity could produce a false operational claim.

### Final repository hygiene audit

Review complete `base...HEAD`.

Classify every added file.

Remove implementation-only artifacts such as internal plans, ChatGPT/Claude instructions, handoffs, temporary ledgers, review screenshots, traces/reports, one-off scripts, caches/logs, orphan fixtures, accidental generated output and local config/paths/secrets.

The three PR coordination documents are temporary branch state and MUST be deleted in Phase 14 before merge readiness.
