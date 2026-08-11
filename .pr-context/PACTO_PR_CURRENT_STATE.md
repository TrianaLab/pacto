# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-11  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`d0dbad91752a15ffac7e56b8cbc5945e705620cb`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Commits reviewed on top of the previous reviewed HEAD `41455305`:

- `93594427` — an owner label is not an owner identity
- `2cef0ebb` — the fleet says who, and says who it could not file
- `43b807c5` — the demo fleet can spell one name two ways
- `4b5cd985` — rebuild the UI bundle
- `d0dbad91` — persist the independently reviewed PR state at `41455305`

Independently verified at this exact SHA: the review of `d0dbad91` accepted the
substance of that work and reopened Phase 5 and Phase 6 for narrow items only.

This section records the last INDEPENDENTLY REVIEWED state. It is not a Claude
self-assessment and must not be re-closed by the session that implements against
it.

### Accepted at this review — do NOT reopen

The following are settled. Do not redesign them, and do not "improve" them as a
side effect of the narrow items in section 2:

**Ownership identity and discovery**

- `OwnerKey` as a namespaced Team/DRI identity — `team/alice` and `dri/alice`
  are DIFFERENT owners;
- `{team: platform, dri: alice}` and `{team: platform, dri: bob}` remain the
  SAME owner: extra fields are metadata about an owner, not identity;
- Team taking canonical precedence over DRI;
- `ownerKey` as the exact canonical filter every canonical link must use;
- `owner` as the fuzzy human-entered search;
- contacts-only ownership having NO fabricated canonical Owner identity;
- the ownership consistent / conflicting / unowned classification;
- owner ranking population semantics generally;
- `unidentifiedOwnership` as a concept separate from the ranking tail;
- the Owners aggregate's independent loading / error / stale-refresh behaviour;
- the injective round-trippable canonical transport encoding and its
  fail-closed handling of raw legacy keys.

**Presentation and structure**

- `PageToc` current-section behaviour and its per-section `scroll-margin-top`;
- the Operational Overview dashboard generally;
- Services aggregate intelligence over the complete filtered population;
- revision-scoped Readiness;
- typography roles;
- progressive disclosure;
- Graph / Change-analysis workspace geometry;
- graph state and Cytoscape behaviour.

**Model**

- lock v3 content-identity occurrence model, the delimiter-collision
  correction, multiple-path fail-closed semantics and the
  duplicate-declaration correction;
- the authoritative denominator holding downward (`Unclassified` remainder)
  and upward (explicit inconsistency when segments sum ABOVE the total).

**Phases**

- Phase 3 and Phase 4 are closed. Do not reopen absent a new concrete
  counterexample.

## 2. Current phase status

### Phase 1 — COMPLETE

No known reopen item.

### Phase 2 — COMPLETE

Accepted identity/Product API foundations remain intact.

### Phase 3 — COMPLETE

Lock v3 content-identity occurrence model, delimiter-collision correction,
multiple-path fail-closed semantics and the duplicate-declaration correction are
all accepted.

### Phase 4 — COMPLETE

Operational Graph, real Cytoscape rendering, projection semantics,
target/revision/service identity, declared/observed distinction, graph controls,
Product graph route, visual browser gate, graph spatial persistence and semantic
refresh without full layout reset are all accepted.

### Phase 5 — NARROWLY REOPENED

The Product target from the previous iterations is substantially ACCEPTED and
must not be redesigned. See section 1 for the accepted set.

Phase 5 is reopened for exactly five items.

**1. Contacts-only declared ownership is still rendered "Unowned" on entity detail**

The backend already distinguishes a declared owner block from an absent one
(`OwnershipInfo.Declared`), and the aggregate already counts contacts-only
ownership as declared. Entity detail does not: Service, Revision and Operational
Target detail still render the canonical Owner link when a canonical identity
exists and the literal word `Unowned` otherwise. A contract that declares an
owner through contact points only therefore reads as having no owner at all, and
the Owners aggregate and the entity page contradict each other on the same
fleet.

The three required display states are: no declared owner; declared with a
canonical Owner identity; declared with NO canonical Team or DRI identity.

No canonical Owner identity may be invented from an email, Slack channel, URL or
other contact point.

**2. `DistributionBar` mishandles an authoritative total of 0 with positive buckets**

The under-count and over-count directions are accepted. The remaining edge is an
authoritative `total = 0` reported alongside a positive bucket sum: the shared
primitive currently renders `3 (0% of 0)`, which reads as a valid distribution
over an empty population. Zero must remain authoritative, the contradictory
positive count must stay visible, and the percentage must be explicitly
unavailable rather than fabricated.

**3. Data Sources discoverability**

Data sources are a first-class Product concept with their own inventory and
entity pages, but on Overview they are a sub-heading buried inside the immediate
situation band. They have no semantic section of their own, so they do not
appear in the Overview `PageToc`, and the path from "a source is degraded" to
"inspect that source" is not obvious to a novice.

Primary navigation stays exactly four tabs. Data sources must NOT become a fifth
primary tab.

**4. Selected-source health versus global Fleet knowledge completeness**

Source detail can currently render a healthy `Available` source in the page
header and, directly beneath it, the global knowledge banner reading
`Source unavailable — this page may be incomplete`. On a Source page that
sentence reads as a claim about the source being inspected. Selected-source
health and Fleet/snapshot knowledge completeness are different facts and must be
scoped separately without weakening the knowledge treatment on Service, Revision
and Target pages.

**5. Source-detail information hierarchy and contribution semantics**

Source detail is currently a compact debug record rather than a Product
inspector. It does not answer, in order: what am I inspecting; is THIS source
healthy and current; when did it last successfully synchronize; when was it last
observed; how much raw knowledge did it supply; what Product entities are
attributable to it; if degraded, what failed; what limitations remain.

Related: `SourceState.RevisionCount` / `SourceState.TargetCount` count DIRECT
records submitted by the source, while `entitiesFromSource` returns Product
entities ATTRIBUTABLE to it (Services are derived, not directly recorded). The
two totals can legitimately differ, and the UI currently gives a reader no way to
know that. The distinction must be defined and taught, not equalized for
presentation.

### Phase 6 — NARROWLY REOPENED

The existing browser suite is accepted and must stay green; no existing browser
acceptance may be removed or weakened.

Reopened because browser acceptance lacks the exact counterexamples for the five
Phase 5 items:

- contacts-only ownership: the aggregate says declared/consistent while entity
  detail must NOT say `Unowned`, must NOT fabricate a canonical Owner link, and
  must still let a reader reach the declared contact information;
- contact-set semantics: `[A]` versus `[A, A]` resolved deliberately, with the
  counterexample written first;
- ranking namespace: same-display Team and DRI owners where only ONE is inside
  the visible top-N, proving visible identity does not depend on the truncation
  boundary;
- distribution zero total: authoritative `total = 0` with a positive bucket
  rendering an explicit inconsistency, a visible positive count, no `0% of 0`
  and nothing resembling a valid distribution;
- Data sources on Overview: a semantic section, present in the `PageToc`,
  visibly navigable named source chips, chip to exact Source detail and a
  section-level action into the complete inventory;
- an Available selected source under a degraded fleet;
- an Unavailable selected source;
- contribution semantics on a fixture where direct record counts genuinely
  differ from the contributed Product entity total — the test must not be made
  to pass by equalizing the fixture;
- Source-detail layout at desktop and mobile, and Back/Forward navigation
  through the complete Data Sources flow.

Tests must be discriminating: at least the essential counterexample must be
demonstrated to fail against pre-fix code.

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

**Data source**
- provenance inspector: what this source is, whether it is healthy and current,
  what it supplied and what is attributable to it.

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

Declared owner CONTACT metadata is part of the contract material a Revision
declares. It must remain reachable in the Revision inspector without being
promoted into a canonical Owner identity.

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

### Blocker A — contacts-only ownership reads as "Unowned"

Detailed in Phase 5 item 1. Acceptance must prove, in the browser and against
the real fleet:

1. a service whose only owner declaration is contact points is NOT presented as
   unowned on Service, Revision or Operational Target detail;
2. no canonical Owner link is fabricated for it;
3. the declared contact information remains reachable in the Revision inspector
   through progressive disclosure;
4. a service with a genuinely absent owner block is still presented as having no
   declared owner;
5. existing canonical Owner links do not regress.

### Blocker B — the authoritative denominator and an authoritative zero

Detailed in Phase 5 item 2. Acceptance must prove that an aggregate reporting a
total of `0` with buckets summing above zero renders an explicit inconsistency
with the positive count intact and no fabricated percentage, through the shared
primitive, in a component test AND on a real browser page.

### Blocker C — data-source comprehension

Detailed in Phase 5 items 3, 4 and 5. Acceptance must prove that a novice can
answer, without searching: which source is degraded; can I inspect it; what does
it contribute. It must also prove that an `Available` selected source is never
presented as if it were itself unavailable.

### Previously accepted and NOT reopened

- namespaced canonical `OwnerKey`;
- fuzzy `owner` search versus exact `ownerKey` filter;
- `unidentifiedOwnership` as its own population;
- ownership classification;
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
- current disclosure accessibility;
- `PageToc` behaviour.

## 8. Latest verification snapshot

Reviewed at exact HEAD `d0dbad91`.

Review-thread and CodeQL status must be re-established at the exact final SHA of
the next pass, AFTER the final generated UI bundle is committed, and reported as
three separate counts: unresolved authored threads; unresolved generated/minified
vendor-bundle threads; threads resolved but outdated.

The Security workflow's own status is a different claim from CodeQL alert
attribution. Do not describe CodeQL alert provenance as independently established
without inspecting the underlying alert records and their base/head evidence.

Counts in a handoff are not evidence.

Important process rule:

**Do not trust reported counts blindly in a later chat. Re-verify exact final SHA, CI and review threads before accepting the next handoff.**

## 9. Historical constraint

Authored-content U+00A7 gate is expected green.

Blocking enforcement over old branch commit history / PR metadata remains constrained because historical branch commit messages already contain the forbidden character.

No permission exists to rewrite shared history.

Do not rebase/filter-history/force-push to solve that unless Eduardo explicitly authorizes it.

## 10. Next iteration objective

This is a NARROW Product-coherence pass. The previous iterations are
substantially accepted and must not be reopened or redesigned.

The immediate next Claude session should:

1. make contacts-only declared ownership readable on Service, Revision and
   Operational Target detail through one shared ownership presentational
   primitive, without fabricating a canonical Owner identity;
2. keep the declared contact information reachable in the Revision inspector
   through progressive disclosure;
3. close contact-set semantics deliberately, with the counterexample written
   first, so docs, implementation and schema agree;
4. make a ranked owner row's visible identity independent of the ranking
   truncation boundary;
5. make an authoritative total of `0` with positive buckets render an explicit
   inconsistency in the shared primitive;
6. promote Data sources to a recognizable Overview section without adding a
   fifth primary tab;
7. separate selected-source health from Fleet knowledge completeness;
8. rebuild Source detail as a Product inspector inside the existing visual
   grammar, and define direct source records versus contributed Product
   entities;
9. add the browser counterexamples for all of the above without weakening the
   existing suite;
10. run the full verification matrix, rebuild the UI bundle cold, and audit the
    review threads AFTER the final bundle commit;
11. do not begin Phase 7.

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
