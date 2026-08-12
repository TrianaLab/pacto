# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-12  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`b3075ece40963bceaa640725583924aaf2bacfe9`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Commits reviewed on top of the previous reviewed HEAD `eedab3f7` — both of them:

- `4ba54a13` — persist the independently reviewed PR state at `eedab3f7`
- `b3075ece` — a refused identity is not a process that failed to start

For completeness, the full appended range since the reviewed HEAD `5f3d4ebb`:

- `d098044b` — persist the independently reviewed PR state at `5f3d4ebb`
- `8b0f26b0` — a data source is exactly the one thing it declares itself to be
- `fa76b69a` — every value the chart accepts survives the trip to the dashboard
- `ae29a13d` — the source boundary is written down, and Kind proves it
- `eedab3f7` — the escaping source gets its own claim
- `4ba54a13` — persist the independently reviewed PR state at `eedab3f7`
- `b3075ece` — a refused identity is not a process that failed to start

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

Implemented at `d18ca70e`. This is a Claude self-report and closes nothing: the
phase is a CANDIDATE until an independent review says otherwise.

What the live cluster now proves, in the cluster and in the browser:

- four bundles published to the in-cluster registry through the real `pacto push`
  (payments, checkout 1.0.0, checkout 1.1.0, orders), each resolved to an
  immutable manifest digest;
- both Pacto CRs resolve `contractRef.oci` at a digest, so the operator's
  `status.contract.resolvedRef` is a real resolved contract identity and the
  dashboard reaches the SAME content back through the registry;
- `tests/e2e/kind/productready` (Go) gates the browser layer on twelve facts
  re-checked every round against the live Product API, and emits the keys it
  DISCOVERED as `PW_FIXTURE`, so no browser journey constructs an identity;
- eight live Product journeys (A–H) in `pkg/dashboard/frontend/e2e-live/`, all
  passing on the Kind `operational-graph` shard at `d18ca70e`.

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
- after a dashboard pod restart, `IncludeCache` plus the OCI source can mint a
  second `RevisionKey` for the same content.

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

Reviewed at exact HEAD `b3075ece`.

Review threads at that SHA:

- 0 unresolved authored-product threads;
- the remaining unresolved `github-code-quality` threads are on GENERATED
  minified UI assets under `pkg/dashboard/ui/assets/`, not authored code.

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
