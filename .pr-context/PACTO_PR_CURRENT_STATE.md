# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-11  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`414553055ca04b58a97165cd78b130aaeee4bc24`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Commits reviewed on top of the previous reviewed HEAD `0a362da6`:

- `78da0e41` — persist the independently reviewed PR state at `0a362da6`
- `e907df00` — an owner link asks who, not who-ish
- `bbbc2978` — the ownership summary can fail out loud
- `ffe0a27f` — every section is read at the line it parks at
- `c6e93ac7` — the owner name, the summary and the reading line, in a browser
- `41455305` — rebuild the UI bundle

Independently verified at this exact SHA: the review of `41455305` accepted the
substance of that work and reopened Phase 5 and Phase 6 for narrow items only.
The accepted set is recorded in section 3 and must not be redesigned:

- the lock v3 content-identity occurrence model;
- the delimiter-collision correction;
- multiple-path fail-closed semantics;
- the duplicate-declaration correction — do not redesign lock v3,
  `DuplicateDeclarations`, declaration occurrence identity or lock traversal
  without a new concrete counterexample;
- the Operational Overview structure;
- complete-population Services aggregates;
- the revision-scoped Readiness model;
- the ownership consistent / conflicting / unowned classification itself;
- that a conflicted service may be discovered by every owner that claims it;
- per-owner ranking links carrying `ownership=consistent`;
- the Owners aggregate concept and its placement on the page;
- the distinction between fuzzy owner SEARCH and an exact owner FILTER;
- `owner` as a human-entered fuzzy search, and `ownerKey` as the concept every
  canonical link must use;
- one Team declared with different DRI or contact metadata being ONE owner;
- the Owners aggregate's independent loading / error / stale-refresh behaviour;
- the authoritative service count being passed to `DistributionBar`, and an
  under-count rendering an `Unclassified` remainder;
- `PageToc` current-section behaviour and its per-section
  `scroll-margin-top` implementation;
- typography roles;
- progressive disclosure;
- rich entity inspection;
- graph state and Cytoscape behaviour;
- Graph / Change-analysis workspace geometry.

This section records the last INDEPENDENTLY REVIEWED state. It is not a Claude
self-assessment and must not be re-closed by the session that implements against
it.

## 2. Current phase status

### Phase 1 — COMPLETE

No known reopen item.

### Phase 2 — COMPLETE

Accepted identity/Product API foundations remain intact.

### Phase 3 — COMPLETE

The lock v3 content-identity occurrence model, the delimiter-collision
correction, multiple-path fail-closed semantics AND the duplicate-declaration
correction are all accepted.

Do not redesign lock v3, `DuplicateDeclarations`, declaration occurrence identity
or lock traversal without a new concrete counterexample.

### Phase 4 — COMPLETE

Accepted.

Operational Graph includes real Cytoscape rendering and previously reviewed corrections for projection semantics, target/revision/service identity, declared/observed distinction, graph controls, Product graph route, visual browser gate, graph state persistence and semantic refresh without full layout reset.

Do not reopen absent a concrete counterexample.

### Phase 5 — NARROWLY REOPENED

The Product target from the previous iterations is substantially ACCEPTED and
must not be redesigned: the Operational Overview structure, complete-population
Services aggregates, the revision-scoped Readiness model, the ownership
consistent / conflicting / unowned classification, the rule that a conflicted
service may be discovered by every owner that claims it, per-owner ranking links
carrying `ownership=consistent`, the Owners aggregate concept and page placement,
`PageToc` current-section behaviour in the normal product, typography,
progressive disclosure, rich entity inspection, graph state and Cytoscape
behaviour, and the shared Graph / Change-analysis workspace geometry.

Phase 5 is reopened for exactly three items.

**1. An owner display label is not an owner identity**

The exact owner filter was introduced, but its key is the display label. Two
different declarations that spell one name collapse into one identity:
`{team: alice}` and `{dri: alice}` both key as `alice`, so one roster row, one
Owner page and one filter answer for two different owners, merging two estates.

Required correction: the canonical owner key must be namespaced by the field
that named the owner, and the ontology must hold in both directions:

- `team/alice` and `dri/alice` are DIFFERENT owners;
- `{team: platform, dri: alice}` and `{team: platform, dri: bob}` remain the
  SAME owner — the extra fields are metadata about an owner, not identity.

Do NOT invent an owner identity from an email, Slack channel, URL or other
contact point merely to make ranking easier.

The canonical transport encoding must be injective and round-trippable for every
value the Owner schema permits: do not prepend a delimiter casually and recreate
a collision class. If compatibility for raw legacy keys is retained it MUST be
fail-closed — resolve only when the raw key identifies exactly one canonical
owner, and never choose one or merge both for an ambiguous `team: alice` versus
`dri: alice`.

The contract-reference documentation states that the canonical key is the raw
team or fallback DRI. That statement is part of the defect and must be corrected
with the code.

**2. Keyless ownership must not disappear**

An owner block that names no team and no DRI — contacts only — is a real
ownership declaration with no canonical identity, so it can occupy no ranking
row. It must not be dropped silently, and it must not be folded into "other
owners", which would mix two different populations: canonical owners omitted
because of the top-N bound, and services whose owner declaration has no
canonical identity at all.

The invariant to hold and to state where a reader can check it:

```text
shown + BeyondRanking + UnidentifiedOwnership == Ownership.Consistent
```

Do not make the ranking include conflicted services just to make the count
agree. Any "N owners" wording must name the exact population N represents.

**3. The authoritative denominator must hold in BOTH directions**

The under-count direction is accepted. The over-count direction is not: when the
segments sum to MORE than the authoritative total, the shared primitive must
render an explicit inconsistency state — never a widened denominator, never a
comfortable 100 percent. The correction belongs in the shared primitive, not in
a page-local special case.

### Phase 6 — NARROWLY REOPENED

The existing browser suite is accepted and must stay green; no existing browser
acceptance may be removed or weakened.

Reopened because browser acceptance lacks the adversarial counterexamples above:

- one fleet spelling one name as two owners: two roster rows, two estates, two
  canonical destinations;
- a canonical owner filter that keeps them apart while typed search still finds
  both;
- one team with two DRIs remaining one owner with one estate;
- ownership that names nobody being counted and stated, not silently dropped;
- owners named only by disagreeing revisions never becoming a ranking total;
- an aggregate whose buckets sum ABOVE its authoritative total rendering an
  explicit inconsistency, in a component test and in a real browser page.

Tests must be discriminating: at least the essential counterexample must be
demonstrated to fail against pre-fix code.

`PageToc` needs no new work in this pass.

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

### Blocker A — an owner display label is used as an owner identity

Detailed in Phase 5 item 1. Acceptance must prove, in the browser and against
the real fleet:

1. a fleet that spells one name two ways lists two owners, not one;
2. each of the two opens its own estate, at its own canonical destination;
3. the canonical filter keeps them apart while a typed search still finds both;
4. one Team declared with two different DRIs is still ONE owner with one estate;
5. the canonical transport encoding round-trips every value the Owner schema
   permits, colons in a name included;
6. no owner identity is invented from a contact point.

### Blocker B — keyless ownership disappears from the picture

Detailed in Phase 5 item 2. Acceptance must prove that ownership naming no team
and no DRI is counted and stated in its own right, that it is never folded into
the beyond-the-bound tail, and that
`shown + BeyondRanking + UnidentifiedOwnership == Ownership.Consistent`
holds on the real fleet.

### Blocker C — the authoritative denominator only holds downward

Detailed in Phase 5 item 3. Acceptance must prove that an aggregate reporting
`services = 8` with buckets summing to 10 renders an explicit inconsistency
state — through the shared primitive, with no page-local special case, in a
component test AND on a real browser page.

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

Reviewed at exact HEAD `41455305`.

At that independently reviewed SHA there were six current unresolved
`github-code-quality` threads on the generated Mermaid gantt asset and no
unresolved authored-product thread.

Review-thread and CodeQL status must be re-established at the exact final SHA of
the next pass, AFTER the final generated UI bundle is committed, and reported as
three separate counts: unresolved authored threads; unresolved generated/minified
vendor-bundle threads; threads resolved but outdated.

The Security workflow's own status is a different claim from CodeQL alert
attribution. Do not describe CodeQL alert provenance as independently established
without inspecting the underlying alert records and their base/head evidence.

The previous Claude handoff for `41455305` had reported the local gate matrix
green. Counts in a handoff are not evidence.

Important process rule:

**Do not trust reported counts blindly in a later chat. Re-verify exact final SHA, CI and review threads before accepting the next handoff.**

## 9. Historical constraint

Authored-content U+00A7 gate is expected green.

Blocking enforcement over old branch commit history / PR metadata remains constrained because historical branch commit messages already contain the forbidden character.

No permission exists to rewrite shared history.

Do not rebase/filter-history/force-push to solve that unless Eduardo explicitly authorizes it.

## 10. Next iteration objective

This is a NARROW correction pass. The previous iterations are substantially
accepted and must not be reopened or redesigned.

The immediate next Claude session should:

1. namespace the canonical owner key by the field that named the owner, with the
   adversarial tests written FIRST, an injective round-trippable transport
   encoding, fail-closed handling of any retained raw key, and the
   contract-reference statement corrected with the code;
2. count and state ownership that names nobody, without folding it into the
   beyond-the-bound tail and without pulling conflicted services into the
   ranking to make an arithmetic agree;
3. make the authoritative denominator hold in both directions in the shared
   primitive, an over-count included;
4. add the browser counterexamples for all of the above without weakening the
   existing suite, and demonstrate that the essential one fails against pre-fix
   code;
5. run the full verification matrix, rebuild the UI bundle cold, and audit the
   review threads AFTER the final bundle commit;
6. do not begin Phase 7.

### Implementation submitted against this review — NOT self-certified

A Claude session has implemented items 1 to 4 above on top of `41455305`. That
work is submitted for independent review; it does NOT re-close Phase 5 or
Phase 6, and this document must not be read as certifying it. Phase 7 was not
started. The next independent review decides what re-closes.

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
