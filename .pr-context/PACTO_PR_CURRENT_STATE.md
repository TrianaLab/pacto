# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-12  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`eedab3f7c1eaebf2d008abadf5d9ca623cda7dba`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Commits reviewed on top of the previous reviewed HEAD `5f3d4ebb` — all five of
them:

- `d098044b` — persist the independently reviewed PR state at `5f3d4ebb`
- `8b0f26b0` — a data source is exactly the one thing it declares itself to be
- `fa76b69a` — every value the chart accepts survives the trip to the dashboard
- `ae29a13d` — the source boundary is written down, and Kind proves it
- `eedab3f7` — the escaping source gets its own claim

This section records the last INDEPENDENTLY REVIEWED state. It is not a Claude
self-assessment and must not be re-closed by the session that implements against
it.

### Handoff discipline — corrected

The handoff that accompanied `5f3d4ebb` enumerated only three commits after
`7da2ad46`; there were four. Omitting the coordination-state commit made the
appended range unverifiable from the handoff alone, which is the one thing the
enumeration exists for.

Every later handoff MUST enumerate EVERY commit between the reviewed starting
SHA and the final SHA of the iteration, including coordination-state and
generated-bundle commits. A commit that only touches `.pr-context/` is still a
commit an independent reviewer has to account for.

### Accepted at this review — do NOT reopen

The following are settled. Do not redesign them, and do not "improve" them as a
side effect of the narrow items in section 2. Reopening any of them requires a
NEW concrete counterexample.

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

- Phase 1 through Phase 6 are closed.

## 2. Current phase status

### Phase 1 — COMPLETE

### Phase 2 — COMPLETE

### Phase 3 — COMPLETE

### Phase 4 — COMPLETE

### Phase 5 — COMPLETE

### Phase 6 — COMPLETE

### Phase 7 — NARROWLY REOPENED at `eedab3f7`

Target:

**operator-managed OFFLINE observation/trace-source configuration**

The offline pipeline (OTLP/JSON trace file, offline `pkg/otelobserver`, observed
edges, Fleet observation source, reconciliation) already exists and is accepted.
What is missing is a declarative, operator-managed way to package, configure and
mount those observation sources, with stable Data Source identity that does not
depend on list position. Phase 7 is that packaging — NOT an OTLP receiver, a
Collector, a trace database or any live ingestion.

The candidate submitted in `e150a548` + `6250ebe3` was reviewed and its DESIGN
was accepted. Everything about Phase 7 stays closed and must not be redesigned
without a new counterexample: the offline analyzer boundary, the operator-managed
observation-source concept, PVC + ConfigMap as the only backings, read-only
mounts, no `hostPath`, deterministic sorting, reordering changing neither
identity nor pod template, complete removal, source failure as explicit
Fleet/Product knowledge, source health separate from observation freshness, the
retained ad-hoc `pacto dashboard --traces`, the focused Kind
observation-packaging scenario (which need NOT manufacture
`relationship.reconciliation == "matched"`), and the existing architecture gates.

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

#### The one remaining Phase-7 blocker — a public-docs claim overstates the failure semantics

The new public documentation says, in at least `docs/operational-graph.md` and
`docs/platform-engineers.md`, that on a Data Source identity collision
"Pacto refuses to start".

That is not the implemented operator-managed dashboard behavior, and the runtime
behavior is NOT to be changed to make the prose true. The implemented and
accepted lifecycle is:

- `Service.Fleet` detects duplicate source ids and returns an error BEFORE
  `fleet.Build`, so no ambiguous `FleetSnapshot` is ever published;
- `fleet.Manager.Start` performs `Refresh`, and a refresh failure is not
  process-fatal: the last good snapshot is retained when one exists, there is no
  snapshot when the first refresh has never succeeded, and the manager keeps
  running and can retry;
- the dashboard HTTP host itself stays alive.

"The process failed to start" is not equivalent to "an ambiguous Product identity
was refused publication". The second is the invariant Phase 7 requires and
implements.

Required correction: fix the prose, in every authored Phase-7 doc that carries
equivalent wording (not only the two known files) — concise where the audience is
not the Operational Graph reference, precise lifecycle semantics in the canonical
Operational Graph documentation. Generated documentation must not be hand-edited.

#### Accepted scoped deviation — still intentional

The live Kind scenario asserts the observed edge under its declared identity and
that it names the same pair the operator reconciled as declared, but not the
snapshot's `reconciliation: "matched"` verdict, which needs a contract-REVISION
source the operator-managed dashboard does not have in that scenario (the live
Kubernetes source projects deployed targets, not revisions). That verdict over an
observation source stays proven hermetically in `internal/app` and by
`make demo-fleet`. The fully live declared+observed Product reconciliation is
Phase 8 work.

### Phase 8 — NOT STARTED

Upgrade the EXISTING live Kind vertical's browser leg from a deliberate smoke
check into representative live Product acceptance.

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

Reviewed at exact HEAD `eedab3f7`.

Review threads at that SHA:

- 0 unresolved authored-product threads;
- the remaining unresolved `github-code-quality` threads are on GENERATED
  minified UI assets under `pkg/dashboard/ui/assets/`, not authored code.

### Cross-cutting PRE-MERGE SECURITY item — OPEN, not Phase-7 scope

Claude reports open CodeQL alerts on `refs/pull/291/head` rather than on `main`.

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
  explicitly dismissed with evidence before Phase 14 readiness.

This item is cross-cutting. It must not be pulled into Phase-7 scope, and Phase 7
must not be blocked on it.

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

Important process rule:

**Do not trust reported counts blindly in a later chat. Re-verify exact final SHA, CI and review threads before accepting the next handoff.**

## 9. Historical constraint

Authored-content U+00A7 gate is expected green.

Blocking enforcement over old branch commit history / PR metadata remains constrained because historical branch commit messages already contain the forbidden character.

No permission exists to rewrite shared history.

Do not rebase/filter-history/force-push to solve that unless Eduardo explicitly authorizes it.

## 10. Next iteration objective

The single remaining Phase-7 blocker in section 2, and nothing else: the public
documentation claims a failure mode the implementation deliberately does not
have. Blockers A, B and C are closed. The accepted Phase-7 implementation, and
Phases 1 through 6, must not be reopened or redesigned as a side effect.

The immediate next Claude session should:

1. correct every authored public sentence that claims Pacto refuses to start on a
   Data Source identity collision, to state what is actually implemented: one
   identity namespace across all assembled Data Sources, a collision rejected
   before an ambiguous snapshot can be published, no arbitrary winner and no
   silent rename, a long-running dashboard that is not killed by a failed
   refresh, normal Manager refresh-failure semantics over a last-good snapshot,
   and no Product Fleet snapshot to serve when none has ever succeeded;
2. audit ALL authored Phase-7 docs for equivalent wording, not only
   `docs/operational-graph.md` and `docs/platform-engineers.md`, and leave
   generated documentation to its generator;
3. keep the concise statement in audience-facing pages and the precise lifecycle
   in the canonical Operational Graph documentation;
4. add a durable executable test ONLY if the four load-bearing semantics are not
   already directly tested — a grep test banning one English phrase is not
   acceptable;
5. change no runtime behaviour, keep every accepted Phase-7 behaviour intact
   including the scoped Kind reconciliation deviation, and not begin Phase 8.

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
