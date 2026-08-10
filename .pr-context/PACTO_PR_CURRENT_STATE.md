# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-10  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`9e76b6bde5360b102b6c57a3d36f92d2034c998d`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Commits reviewed on top of the previous reviewed HEAD `759845ca`:

- `bf3e39d4` — lock reference identified by the contract that declared it
- `a372ffed` — regenerated dashboard SDK for the reference occurrence field
- `e19ee1eb` — one typography system + disclosure that loses nothing
- `754e418e` — rebuilt dashboard UI bundle
- `5468a650` — temporary PR coordination context added
- `81829c55` — e2e lock read by occurrence rather than by label
- `9e76b6bd` — Phase 6 re-closure documentation

Independently verified at this exact SHA:

- the `9e76b6bd` PR workflows are green;
- six review threads are unresolved and all six are generated/minified Mermaid
  bundle findings;
- zero unresolved authored-code threads.

This section records the last INDEPENDENTLY REVIEWED state. It is not a Claude
self-assessment and must not be re-closed by the session that implements against
it.

## 2. Current phase status

### Phase 1 — COMPLETE

No known reopen item.

### Phase 2 — COMPLETE

Accepted identity/Product API foundations remain intact.

### Phase 3 — NARROWLY REOPENED

Two narrow items remain.

**1. Non-injective reference occurrence-path identity**

The previous fix correctly proved that `kind + name` is insufficient in a
transitive lock closure, and lock v2 added `Reference.From`, documenting
`From + Kind + Name` as one unique reference occurrence.

That claim is still false.

`ReferencePath` is conceptually:

```text
segment = kind + ":" + name
path    = parent + "/" + segment
```

Configuration and policy names in the current Pacto v2 schema accept arbitrary
non-empty strings, and `/` and `:` are legal. The serialized path is therefore
not injective.

Counterexample shape:

```text
root
  config name "a/policy:b"   -> contract X
  config name "a"            -> contract C

contract C
  policy name "b"            -> contract Y
```

The declaring-contract path of X is `config:a/policy:b`. The declaring-contract
path of Y is also `config:a/policy:b`. If both X and Y then declare a config
named `settings`, two distinct declarations serialize to the identical tuple
`From = "config:a/policy:b"`, `Kind = "config"`, `Name = "settings"`.

Nuance: `RootReference` is currently protected from this specific collision
because it only accepts `From == ""`. The defect is NOT that the current
root-only Fleet lookup necessarily returns the wrong destination. The defect is
that the serialized lock ONTOLOGY cannot uniquely represent all the valid
transitive occurrences it claims to identify.

**2. Declaration identity vs traversal provenance is not explicit**

Three concepts are currently conflated by using a traversal path as an
occurrence's declaring identity:

- A. declaring contract identity — which immutable contract holds the
  declaration;
- B. declaration identity — which config/policy declaration inside that
  contract;
- C. traversal provenance — through which root -> ... path that declaring
  contract was reached.

`buildReferenceClosure` deduplicates recursion by resolved bundle identity, so a
contract reachable through two closure paths is recursed into only through
whichever path arrives first. The ontology must state whether a declaration is
one occurrence or several, and where plural provenance paths live.

Required correction:

- an occurrence representation that is injective for EVERY name the contract
  schema accepts;
- do not restrict legal names merely to save the current encoding;
- an explicit, documented separation of declaration identity from traversal
  provenance;
- a proven duplicate-same-kind-name invariant;
- a deliberate lock-version compatibility decision;
- preserved deterministic lock generation and cycle safety.

### Phase 4 — COMPLETE

Accepted.

Operational Graph includes real Cytoscape rendering and previously reviewed corrections for projection semantics, target/revision/service identity, declared/observed distinction, graph controls, Product graph route, visual browser gate, graph state persistence and semantic refresh without full layout reset.

Do not reopen absent a concrete counterexample.

### Phase 5 — NARROWLY REOPENED

The previous presentation blockers are RESOLVED and ACCEPTED:

- the typography role system itself is ACCEPTED;
- the progressive-disclosure primitive introduced in the previous session is
  ACCEPTED.

Do not redesign either without a new concrete counterexample.

Phase 5 is reopened only for the newly-authorized Product target:

1. a richer Operational Overview dashboard;
2. complete-population Services inventory intelligence;
3. Product-native Ownership aggregate insights;
4. Product-native revision-scoped Readiness insights;
5. a shared long-page "On this page" navigation primitive;
6. a shared Operational Graph / Change analysis workspace geometry;
7. correction of stale `Deployment` terminology in the current architecture
   model.

These are authorized Product requirements. The previous broad Product-design
freeze must not be used to defer them.

### Phase 6 — NARROWLY REOPENED

Reopened for browser acceptance of the Phase-3 and Phase-5 work listed above.

The previously accepted browser acceptance (typography computed styles,
disclosure accessibility, rich journeys) remains accepted and must stay green.

### Phase 7 — NOT STARTED

Do not start until Phases 3/5/6 re-close.

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

## 3. Accepted Product architecture

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

## 4. Accepted information-parity work

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

The prior lazy read incorrectly used a live `os.DirFS`, allowing the same RevisionKey to return changed bytes after Build.

Latest accepted correction:

- Build records SHA-256 fingerprint for listed document content;
- lazy read verifies bytes against fingerprint;
- changed/deleted/unreadable body becomes explicit unavailable;
- 512 KiB bound remains;
- source conflicts do not silently pick arbitrary bytes.

Do not reopen absent a new counterexample.

## 5. Accepted UI/interaction corrections

### Search autocomplete

Services search has Product-backed suggestions.

Expected behavior:

- debounced;
- bounded;
- canonical identity;
- same-name domain disambiguation;
- keyboard/pointer;
- stale-response protection.

### Same-query SWR

Accepted model:

- first load / changed identity may load;
- same-query refresh retains last valid data;
- refresh failure retains stale data with honest notice;
- changed query must not present old rows as if they answered the new query.

Latest implementation added explicit query identities to list views and a shared stale refresh notice.

### Scroll restoration

Accepted model:

- scroll belongs to a history entry, not only URL;
- same URL in different history entries remains independent;
- push starts fresh;
- Back/Forward restores exact entry;
- hard reload restores current entry;
- canonical replace preserves entry;
- state bounded;
- user scroll input can cancel pending restoration.

### Graph spatial state

Accepted from prior review:

- same-query refresh preserves layout;
- reload restores graph spatial state;
- query identities isolate graph state;
- semantic data still refreshes;
- `Fit` and `Reset layout` are different.

## 6. Accepted visualization direction

The redesign initially became too list/table-heavy.

Accepted principle:

> lists/tables for exact inspection; visualizations for system comprehension.

Useful visual summaries were added/evolved across Product surfaces.

Do not restore V1 dashboards mechanically.

Any future chart must:

- answer a real question;
- use authoritative aggregate data;
- not infer global truth from a page;
- preserve exact values;
- expose uncertainty/completeness;
- remain accessible/mobile/light/dark.

## 7. Current high-priority blocker details

### Blocker A — reference occurrence identity is not injective

Detailed in Phase 3 above. The lock now records a declaring path, but that path
is a `/`- and `:`-joined string over names that may themselves contain `/` and
`:`, so two distinct declarations can serialize to one tuple.

Required next iteration:

1. write the delimiter-collision counterexample BEFORE fixing it;
2. make the occurrence representation injective for every schema-legal name;
3. separate declaring-contract identity, declaration identity and traversal
   provenance explicitly;
4. audit multiple closure paths to one immutable contract;
5. prove the duplicate-same-kind-name invariant instead of assuming the prose
   description is enforcement;
6. decide lock-version compatibility deliberately rather than silently
   reinterpreting written v2 data;
7. regenerate deliberate lock fixtures; second regeneration byte-identical;
8. no frontend fix for backend identity problems.

### Blocker B — Product target gap: aggregate intelligence

Overview, Services, Ownership and Readiness lack Product-native aggregate
comprehension over honest complete populations. Detailed in the target-state
deltas.

### Blocker C — Product target gap: long-page navigation

Revision / Service / Operational Target remain long after progressive
disclosure, with no intra-page navigation primitive.

### Blocker D — Product target gap: workspace geometry

Operational Graph and Change analysis are sibling primary workflows that
currently compose the same conceptual shape with different geometry.

### Previously accepted and NOT reopened

- immutable Revision document semantics;
- Markdown/Mermaid Product viewer;
- Product information parity;
- repository-basename reference heuristic removal;
- `ReferenceRef.Name` heuristic removal;
- query-aware stale-while-revalidate;
- scroll restoration;
- graph spatial persistence;
- graph semantic refresh;
- Services autocomplete;
- Product API boundedness;
- generated SDK as wire authority;
- current typography role hierarchy;
- current disclosure accessibility.

## 8. Latest verification snapshot

Independently verified at exact HEAD `9e76b6bd`:

- the PR workflows are green;
- six review threads unresolved, all six generated/minified Mermaid bundle
  findings;
- zero unresolved authored-code threads.

The previous Claude handoff for `759845ca` had reported gofmt/vet/lint/gocyclo
clean, 100.0% coverage gates, `svelte-check` 0 errors + 15 pre-existing
warnings, Vitest 1116 passed / 66 files, Playwright 167 passed / 18 specs, 0
`test.fixme` and 1 data-guard `test.skip`.

Important process rule:

**Do not trust reported counts blindly in a later chat. Re-verify exact final SHA, CI and review threads before accepting the next handoff.**

## 9. Historical constraint

Authored-content U+00A7 gate is expected green.

Blocking enforcement over old branch commit history / PR metadata remains constrained because historical branch commit messages already contain the forbidden character.

No permission exists to rewrite shared history.

Do not rebase/filter-history/force-push to solve that unless Eduardo explicitly authorizes it.

## 10. Next iteration objective

The immediate next Claude session should:

1. reproduce the delimiter collision, then make reference occurrence identity
   injective at the model level;
2. make declaration identity vs traversal provenance explicit;
3. prove the duplicate-declaration-name invariant;
4. decide lock-version compatibility deliberately and regenerate fixtures
   deterministically;
5. correct stale `Deployment` wording in the current architecture model;
6. build the richer Operational Overview over honest complete populations;
7. add complete-filtered-population Services inventory intelligence backed by a
   bounded Product aggregate query;
8. restore Product-native Ownership aggregate insights without a fabricated
   composite owner health score;
9. add Product-native revision-scoped Readiness insights whose unit is always
   Contract Revision;
10. add one shared "On this page" primitive that cannot drift from the rendered
    sections and cannot corrupt the hash router;
11. unify Operational Graph and Change analysis onto one workspace scaffold;
12. run the browser cognitive walkthrough and the full acceptance matrix;
13. do not begin Phase 7.

The design constraint is:

```text
fewer things competing simultaneously
  + better visual summaries
  + progressive disclosure
  + direct drill-down
```

NOT `existing content + many more charts visible at once`.

Once those re-close, freeze broad Product UI work again and proceed to Phase 7.

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
