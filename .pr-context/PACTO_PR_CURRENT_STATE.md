# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-12  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`7da2ad4640c7f84d7123fc20a2c6154f63765f5d`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Commits reviewed on top of the previous reviewed HEAD `ea73e8e8`:

- `e090fe73` — persist the independently reviewed PR state at `ea73e8e8`
- `03ab3c10` — the caveat counts the fleet, not the fifty it was shown
- `ef67f494` — stop tracking one workstation's agent tooling
- `fffd9a39` — drop seventeen module hashes nothing in this branch needs
- `ccf2ae0e` — stop tracking a 47MB build output
- `9f407503` — rebuild the UI bundle
- `7da2ad46` — a bucket is not released while it is still being read

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

### Phase 7 — NOT STARTED at `7da2ad46`

Target:

**operator-managed OFFLINE observation/trace-source configuration**

The offline pipeline (OTLP/JSON trace file, offline `pkg/otelobserver`, observed
edges, Fleet observation source, reconciliation) already exists and is accepted.
What is missing is a declarative, operator-managed way to package, configure and
mount those observation sources, with stable Data Source identity that does not
depend on list position. Phase 7 is that packaging — NOT an OTLP receiver, a
Collector, a trace database or any live ingestion.

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

Reviewed at exact HEAD `7da2ad46`.

Review threads at that SHA:

- 0 unresolved authored-product threads;
- the remaining unresolved `github-code-quality` threads are on GENERATED
  minified UI assets under `pkg/dashboard/ui/assets/`, not authored code.

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

Phase 7 — operator-managed OFFLINE observation/trace-source configuration.
Everything in section 1 is accepted and must not be reopened or redesigned.

The immediate next Claude session should:

1. give an observation source an EXPLICIT, stable identity that survives
   reordering and reaches the Product Data Source, replacing list-position ids
   for declaratively configured sources;
2. add the smallest coherent operator-managed model for mounting offline trace
   files read-only from externally managed Kubernetes storage;
3. wire it end to end: Helm values, values schema, controller flags, dashboard
   config, Deployment volumes/mounts/env, Fleet observation sources;
4. keep configured-source failure as Product knowledge (a degraded/unavailable
   Data Source), never a dashboard crash or a silently absent source;
5. keep source health and observed-evidence freshness distinct;
6. prove the packaging with a FOCUSED live Kind scenario, including one forced
   source failure — not the broad Product journey reserved for Phase 8;
7. keep and strengthen the offline architecture gate: no OTLP receiver, no
   listener, no collector sidecar;
8. document the boundary honestly and correct any comment that got ahead of the
   implementation;
9. not begin Phase 8.

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
