# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-11  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`ecb96d3cfdfee8aa7e444cbd9f7cc78e93782cd9`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Commits reviewed on top of the previous reviewed HEAD `9e76b6bd`:

- `969c165a` — reference occurrence identity made injective (lockVersion 3)
- `2a54bcba` — one noun for an operational target, and the Knowledge control
- `45a898d5` — backend-authoritative ownership and readiness inventory
- `43adf209` — the whole question answered on Overview, Services and the inventories
- `e2d25d17` — one shared "On this page" navigator, Owners reachable without a nav slot
- `4f7349d1` — one page scaffold, so two workspaces stop drifting apart
- `8ff4d8a8` — page scaffold measured in a browser
- `e5552bca` — figures audited on every page that draws one
- `5141117f` — a zero is an answer, and a number belongs beside its label
- `fe41a5c1` — contents navigator measured in a browser
- `3f64d63c` — the list a reader came for, above the analysis of it
- `11798e20` — figures and rows proven to describe one population
- `714ded1f` — the semantics a reader can rely on, as implemented
- `ddbb7dfc` — the section sign spelled out, as the gate requires
- `6bd5c76f` — rebuilt dashboard UI bundle
- `68127659` — a comment run parsed once, not once per comment
- `ecb96d3c` — e2e polls for the degraded store, like every other restart assertion

Independently verified at this exact SHA: the review of `ecb96d3c` accepted the
substance of that work and reopened three narrow items only. The accepted set is
recorded in section 3 and must not be redesigned:

- the lock v3 content-identity occurrence model;
- the delimiter-collision correction;
- multiple-path fail-closed semantics;
- the Overview three-band dashboard;
- complete-filtered Services aggregates;
- the revision-scoped Readiness model;
- typography roles;
- progressive disclosure;
- graph spatial persistence;
- Product information parity;
- Graph / Change-analysis workspace geometry.

This section records the last INDEPENDENTLY REVIEWED state. It is not a Claude
self-assessment and must not be re-closed by the session that implements against
it.

## 2. Current phase status

### Phase 1 — COMPLETE

No known reopen item.

### Phase 2 — COMPLETE

Accepted identity/Product API foundations remain intact.

### Phase 3 — NARROWLY REOPENED

The lock v3 content-identity occurrence model is ACCEPTED. The delimiter
collision is fixed, the declaring identity is a content identity rather than a
delimiter-joined traversal path, and multiple-path arrival fails closed.

One narrow item remains.

**Duplicate same-name declarations can still collapse in `pacto lock`**

Canonical cross-field validation rejects duplicate configuration and policy
names (`DUPLICATE_CONFIGURATION_NAME`, `DUPLICATE_POLICY_NAME`), but `pacto lock`
does not run that validation before building the reference closure. The closure
guard in `buildReferenceClosure` treats a second arrival at the same occurrence
tuple as a repeated traversal:

```go
if prev, dup := seen[entry.Occurrence()]; dup {
    if prev != entry { return &lock.AmbiguousError{...} }
    continue // silently collapses two byte-identical DECLARATIONS
}
```

Two duplicate declarations of the same `(kind, name)` in ONE contract that
happen to resolve to identical bytes therefore produce a single lock entry, which
contradicts the v3 claim of one entry per declaration occurrence. The
`AmbiguousError` doc already asserts the closure builder is the last gate for
duplicate names; for identical bytes that assertion is currently untrue.

Required correction:

- write the failing counterexample FIRST, for BOTH configuration and policy;
- enforce that a declaring contract may contain at most one declaration for a
  given `(kind, name)`;
- reject a second declaration regardless of whether it happens to resolve to
  identical bytes;
- do not make `pacto lock` perform unrelated remote or full validation as a side
  effect;
- ordinary non-duplicate names keep working;
- deterministic v3 regeneration stays byte-identical;
- no lock-version bump unless the serialized wire actually changes.

### Phase 4 — COMPLETE

Accepted.

Operational Graph includes real Cytoscape rendering and previously reviewed corrections for projection semantics, target/revision/service identity, declared/observed distinction, graph controls, Product graph route, visual browser gate, graph state persistence and semantic refresh without full layout reset.

Do not reopen absent a concrete counterexample.

### Phase 5 — NARROWLY REOPENED

The Product target from the previous iteration is substantially ACCEPTED and must
not be redesigned: the Overview three-band dashboard, complete-filtered Services
aggregates, the revision-scoped Readiness model, typography roles, progressive
disclosure, graph spatial persistence, Product information parity and the shared
Graph / Change-analysis workspace geometry.

Phase 5 is reopened for exactly three items.

**1. PageToc has no active/current section**

`PageToc.svelte` discovers its entries from the rendered DOM and jumps correctly,
but nothing tells the reader where they currently are. There is no active entry,
no `aria-current`, and no scroll geometry at all. Requirements: the current
section is identified as the reader scrolls; choosing an entry makes that section
current; the current entry updates again on scrolling into the next section; the
distinction is not carried by colour alone; the state is exposed accessibly
(preferably `aria-current`); behaviour at section boundaries is deterministic; no
hash or history mutation; no fighting a programmatic smooth scroll; no separate
mobile implementation; disclosure semantics stay intact when the current section
is inside a collapsed disclosure; sticky-header offsets are accounted for. One
deterministic selection rule — not an observer whose current item oscillates
because several sections intersect at once.

**2. Owners inventory has no aggregate ownership insight**

`FleetOwnersView` is still search + paged list + pager. Above the inventory it
needs: consistent ownership coverage; conflicting ownership; unowned services;
and a bounded top-owner distribution with truthful remainder semantics. The
populations must be backend-authoritative and complete — never derived from the
current owner page — and must reuse the existing aggregate machinery rather than
introduce a fourth ownership model. The owner list stays the primary inspection
surface; every interactive aggregate drills into the exact population it counted.

**3. Owner filtering and ownership aggregation disagree semantically**

The Consistent / Conflicting / Unowned partition is accepted, but
`EntityFilter.Owner` matches only `ServiceRecord.Owner`, which is a SUMMARY owner
derived from the lowest-keyed revision. Counterexample: service `disputed`,
revision A declares `team-x`, revision B declares `team-y`. The aggregate
classifies it CONFLICTING and the per-owner ranking correctly excludes it, but a
ranking row drills down with `owner=<team>` alone, so the service can reappear
through its arbitrary summary owner — the bar count and its own drill-down
disagree.

Fix the ontology, not the symptom. Recommended semantics: `owner=<x>` means at
least one revision of the service declares or matches x; a ranking that means
"consistently-owned services for x" drills down with BOTH `owner=<x>` and
`ownership=consistent`. Every owner-answering surface must obey one rule:
Services owner filter, per-owner ranking drill-down, Owner entity
service/revision/target estate, Owner attention links, owner discovery and
ownership conflicts. `ServiceRecord.Owner` is not an authoritative substitute for
the complete set of revision ownership declarations wherever the question is
about ownership.

### Phase 6 — NARROWLY REOPENED

The existing browser suite is accepted and must stay green; it must not be
weakened to accommodate the new work.

Reopened because browser acceptance omitted the counterexamples above:

- Page TOC: exactly one current entry on a long revision page; the current entry
  changes on scrolling into a later section; choosing a different entry opens it
  if necessary and makes it current; the route hash and history length are
  unchanged; representative mobile behaviour.
- Ownership: the Owners aggregate is complete-population and not page-scoped;
  conflicting and unowned coverage drill into rows that match the bucket; a
  top-owner row drills into a list whose total equals the row count; a conflicted
  service proves the chosen owner-filter semantics; paging the owner list does
  not alter the aggregate.

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

### Blocker A — duplicate identical declarations collapse in `pacto lock`

Detailed in Phase 3 above. Acceptance must prove all six:

1. a duplicate configuration name resolving to the SAME ref is rejected;
2. a duplicate configuration name resolving to a DIFFERENT ref is rejected;
3. a duplicate policy name resolving to the same ref is rejected;
4. a duplicate policy name resolving to a different ref is rejected;
5. ordinary non-duplicate names still lock;
6. v3 deterministic regeneration remains byte-identical, with no lock-version
   bump unless the serialized wire actually changes.

### Blocker B — one ownership semantic model end to end

Detailed in Phase 5 item 3. Acceptance must prove all six:

1. `ownership=conflicting` contains the disputed service;
2. `owner=team-x` can discover the disputed service;
3. `owner=team-y` can discover the disputed service;
4. `owner=team-x` + `ownership=consistent` excludes the disputed service;
5. clicking a per-owner ranking produces a list whose total equals that ranking
   count;
6. reversing revision and source order cannot change any of these answers.

### Blocker C — Owners workspace has no aggregate insight

Detailed in Phase 5 item 2. Backend-authoritative complete populations, reusing
the existing aggregate machinery; not a wall of charts; every interactive
aggregate drills into the exact population it counted.

### Blocker D — PageToc has no active/current section

Detailed in Phase 5 item 1. One deterministic selection rule, an accessible
current state, a non-colour distinction, and no second mobile implementation.

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

Reviewed at exact HEAD `ecb96d3c`. The currently-open review threads are
generated/minified vendor bundle findings, not authored-product defects. The
external CodeQL alert count must not be claimed as fixed or introduced without
establishing its base/head provenance.

The previous Claude handoff for `9e76b6bd` had reported gofmt/vet/lint/gocyclo
clean, 100.0% coverage gates, `svelte-check` 0 errors with pre-existing warnings
only, the full Vitest suite green and a 171-test Playwright browser suite green
across desktop and mobile.

Important process rule:

**Do not trust reported counts blindly in a later chat. Re-verify exact final SHA, CI and review threads before accepting the next handoff.**

## 9. Historical constraint

Authored-content U+00A7 gate is expected green.

Blocking enforcement over old branch commit history / PR metadata remains constrained because historical branch commit messages already contain the forbidden character.

No permission exists to rewrite shared history.

Do not rebase/filter-history/force-push to solve that unless Eduardo explicitly authorizes it.

## 10. Next iteration objective

This is a NARROW correction pass. The previous iteration is substantially
accepted and must not be reopened or redesigned.

The immediate next Claude session should:

1. write the duplicate-identical-declaration counterexample first, for BOTH
   configuration and policy, then close the `pacto lock` hole;
2. unify owner semantics end to end so filtering and aggregation answer the same
   question, and prove it with the disputed-service counterexample;
3. give the Owners workspace a backend-authoritative aggregate above the
   inventory, drilling into the exact population it counted;
4. give PageToc a deterministic active/current section with an accessible,
   non-colour-only state;
5. add the browser acceptance for both the TOC and the ownership counterexamples
   without weakening the existing suite;
6. run the full verification matrix and audit the review threads paginated;
7. do not begin Phase 7.

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
