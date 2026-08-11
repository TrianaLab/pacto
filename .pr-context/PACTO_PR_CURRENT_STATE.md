# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-11  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`ea73e8e8390b410e16ecc515401c446e65542b9d`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Commits reviewed on top of the previous reviewed HEAD `d0dbad91`:

- `b5239869` — persist the independently reviewed PR state at `d0dbad91`
- `acbd0e67` — saying a contact point twice does not create a second owner
- `904b8d74` — the fleet carries contacts, keeps namespaces and counts twice over
- `9b6dc4d3` — declaring an owner nobody can name is not the same as declaring nobody
- `244d51b1` — count the sources the list was cut from
- `30a029b9` — a data source is a place you can go, not a caption
- `3ea5ea7c` — walk the data sources in a real browser
- `a8b4a101` — records sent, entities contributed, and an owner nobody can name
- `42a1df00` — rebuild the UI bundle
- `ea73e8e8` — a page that is still loading is not a settled page

This section records the last INDEPENDENTLY REVIEWED state. It is not a Claude
self-assessment and must not be re-closed by the session that implements against
it.

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

**Phases**

- Phase 1 through Phase 4 are closed.

## 2. Current phase status

### Phase 1 — COMPLETE

### Phase 2 — COMPLETE

### Phase 3 — COMPLETE

### Phase 4 — COMPLETE

### Phase 5 — NARROWLY REOPENED

Exactly one Product-semantic blocker remains.

**Knowledge state does not use the complete source population**

The backend now correctly publishes `ProductMeta.SourceCounts` over EVERY source
in the snapshot. It exists because `ProductMeta.Sources` is bounded to
`MaxMetaSources` and is explicitly only a preview, and the backend carries a
regression proving those populations differ.

Frontend `snapshotKnowledge()` still derives `degradedSources`, `staleSources`
and `unavailableSources` by iterating only `meta.sources`. Its structural
`MetaLike` does not consume `sourceCounts`. The Product therefore has TWO
answers to the same source-health population.

Counterexample:

```text
61 sources total
60 unavailable
 1 available

backend           sourceCounts.total = 61, sourceCounts.unavailable = 60
bounded preview   50 entries, all unavailable

Data Sources tally    60 unavailable
KnowledgeBanner       50 data sources are unavailable
```

The level is still `unavailable`, but the quantified explanation is false.

Required invariant:

```text
ProductMeta.SourceCounts = authoritative complete population for source-health arithmetic
ProductMeta.Sources      = bounded named preview for inspection/navigation
```

When `sourceCounts` is present the complete counts drive
`degradedSourceSummary` / `KnowledgeBanner` and the strictest source-health
level. When it is absent an explicit compatibility fallback to `meta.sources`
remains, and that fallback must NOT be presented as complete when
`sourcesTruncated` is known true.

The existing Go `SourceCounts` test uses 1 unavailable and 60 available. Because
the bounded preview prioritizes least-healthy sources, the single unavailable
source survives the cut, so that fixture cannot expose the frontend defect. A
discriminating fixture needs at least one health bucket that itself exceeds
`MaxMetaSources`.

### Phase 6 — NARROWLY REOPENED

The existing browser suite is accepted and must stay green; no existing browser
acceptance may be removed or weakened.

Reopened only for the missing acceptance counterexample: one discriminating
built-WASM/browser or controlled Product-API case where `sourceCounts` differs
from the counts derivable from `meta.sources` because the source preview is
capped, proving on the SAME rendered Product state that the Data Sources
complete-population tally, the global `KnowledgeBanner` and the knowledge
severity all describe the same authoritative population.

The Product must never render simultaneously `60 unavailable` and
`50 data sources are unavailable` for the same Fleet snapshot. The test must not
be made to pass by shrinking the source population below the cap.

### Phase 7 — NOT STARTED

Do not start until Phases 5 and 6 re-close.

Target:

**operator-managed observed/trace-source packaging/config**

Current observed trace ingestion is still conceptually an offline/ad-hoc trace-file capability in important paths. Phase 7 should make it operationally manageable without turning Pacto into an observability backend.

### Phase 8 — NOT STARTED

Live Kind product vertical breadth.

### Phase 9 — NOT STARTED

Real built MkDocs browser E2E.

### Phase 10 — NOT STARTED

Docker Desktop/containerd/local-registry Kind path.

### Phase 11 — NOT STARTED

MCP catalog core.

### Phase 12 — NOT STARTED

MCP discovery server/CLI/E2E/docs.

### Phase 13 — NOT STARTED

Normative invariants finalization.

### Phase 14 — NOT STARTED

Finalization, final ontology audit, repository hygiene, PR body, exact SHA, readiness.

## 3. Branch-hygiene regression

Independent review found that commit `acbd0e67` accidentally added files
unrelated to the requested Product work:

```text
.claude/CLAUDE.md
.codex/config.toml
.mcp.json
AGENTS.md
```

They were not reported in the handoff as intentional repository changes. They
are Repowise / Claude / Codex local-agent tooling: the config files include a
workstation-specific absolute path, and the generated instruction files contain
tool-specific guidance tied to an old Repowise index snapshot.

They MUST NOT remain tracked by this PR. They must be removed with a normal
append-only cleanup commit — `acbd0e67` must not be rewritten. Local copies may
be preserved as UNTRACKED local tooling. Their contents must not be moved into
public docs, and repository policy must not be broadened merely to justify their
presence.

The same commit also added `go.work.sum` entries even though that iteration made
no corresponding dependency-declaration change. The preferred proof is to
restore `go.work.sum` to the `d0dbad91` state, run the clean deterministic
repository commands/gates that legitimately own workspace dependency state, and
observe whether they regenerate the entries. If nothing deterministic requires
them, they stay reverted. Accumulated workstation module state is not retained
merely because it is harmless.

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

Reviewed at exact HEAD `ea73e8e8`.

Review threads at that SHA:

- 0 unresolved authored-product threads;
- 6 unresolved, CURRENT, non-outdated `github-code-quality` threads on the
  generated asset `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7--EkgrGfx.js`;
- older `D5F8W3En` threads are resolved/outdated.

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

This is a VERY NARROW correction/cleanup pass. Everything in section 1 is
accepted and must not be reopened or redesigned.

The immediate next Claude session should:

1. fix the epistemic boundary so `ProductMeta.SourceCounts` is the authoritative
   complete population for source-health arithmetic and `ProductMeta.Sources`
   stays a bounded named preview;
2. keep an explicit, small compatibility fallback for an absent `sourceCounts`,
   and stop that fallback from claiming completeness when `sourcesTruncated` is
   known true;
3. add a discriminating unit fixture where a health bucket itself exceeds
   `MaxMetaSources`;
4. add one Product acceptance counterexample on a capped source population;
5. remove the four local/agent tooling files from tracking with an append-only
   commit, preserving them locally as untracked;
6. audit and decide the incidental `go.work.sum` change with deterministic
   evidence;
7. verify the `d0dbad91...HEAD` diff carries no workstation/local artifacts;
8. run the full verification matrix, rebuild the UI bundle cold, and re-audit
   review threads AFTER the final bundle commit;
9. not begin Phase 7.

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
