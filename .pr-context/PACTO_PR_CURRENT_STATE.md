# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-11  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`0a362da614f5f2f140d33d39c5cd6d2e6a1d354c`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Commits reviewed on top of the previous reviewed HEAD `ecb96d3c`:

- `ea59982b` — persist the independently reviewed PR state at `ecb96d3c`
- `fa6c7e49` — two declarations of one name are two declarations
- `bdce8da0` — one ownership question, asked the same way everywhere
- `7a9ffffb` — tell the reader whether the fleet is owned, not just who exists
- `6ccefd62` — the contents list says where the reader is
- `6b789e66` — the two claims only a browser can settle
- `6d67b8b2` — rebuilt dashboard UI bundle
- `1df6e159` — cover the duplicate-declaration rule where it is stated
- `0a362da6` — let the page hear the jump before navigating away from it

Independently verified at this exact SHA: the review of `0a362da6` accepted the
substance of that work, closed Phase 3, and reopened Phase 5 and Phase 6 for
narrow items only. The accepted set is recorded in section 3 and must not be
redesigned:

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
- `PageToc` current-section behaviour in the normal product;
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

Phase 5 is reopened for exactly four items.

**1. Canonical Owner identity disagrees with fuzzy owner filtering**

Two different meanings hide behind one owner string. Canonical Owner detail
resolves an owner by exact display key (`revision.Owner.DisplayString() == key`),
while `EntityFilter.Owner` resolves by `Owner.MatchesFilter(owner)`, which is a
case-insensitive SUBSTRING over team, DRI and contact values.

Counterexample: owners `team-a` and `team-a-platform`. A canonical action taken
from `/fleet/owners/team-a` that navigates with `owner=team-a` may include
services owned only by `team-a-platform`. The canonical Owner page and its own
drill-down then disagree about what that Owner owns.

Required correction: separate canonical Owner identity from free-text owner
matching. Useful fuzzy user search must NOT be removed. A reasonable design is an
exact owner-key filter for canonical links/actions alongside the existing fuzzy
owner query for user-entered search; the exact API shape is an implementation
decision, but the ontology must be explicit.

Every owner-scoped Product path must be audited: Owner detail; Owner service
estate; revision estate; target estate; Owner attention links; the Services owner
filter; ownership ranking drill-downs; owner discovery; global/free-text owner
search.

The identity case must also be forced, not hand-waved as presentation: revision A
declaring `{team: platform, dri: alice}` and revision B declaring
`{team: platform, dri: bob}` are DIFFERENT under structured `Owner.Equal` but
collapse to one `DisplayString` of `platform`. Either prove the canonical Owner
identity intentionally includes the structured distinction, or document and test
the intended normalization rule.

**2. Owners aggregate load/refresh failures are silent**

`FleetOwnersView` gives the ownership aggregate its own loader, whose failure
state the page never surfaces.

- Counterexample A: the owner roster succeeds and the aggregate fails on first
  load. The page silently omits the whole-fleet ownership picture.
- Counterexample B: a previous aggregate is on screen and the refresh fails. The
  reader keeps reading a stale figure with no notice. Stale-while-revalidate is
  only honest when the page visibly says the refresh failed.

Required correction: explicit aggregate state — initial loading; ready;
unavailable/error; stale data after a refresh failure — reusing the Product's
existing stale/error semantics rather than a new mechanism. Failure of the
aggregate must not unnecessarily destroy the usable Owner roster.

**3. Owners ownership coverage suppresses the authoritative denominator**

`DistributionBar` already renders the remainder as `Unclassified` when
`total > sum(known buckets)`. `FleetOwnersView` defeats that safeguard by passing
`total = segmentTotal(segments)`, so the bars are always drawn against their own
sum and can never show a gap.

The authoritative denominator is the aggregate's service count. Healthy data
still satisfies `consistent + conflicting + unowned == services`; a wire
violation must be exposed rather than normalized away. Do not special-case the
Owners page around the shared `DistributionBar`.

**4. `PageToc`'s documented per-section reading-line rule is not actually per-section**

The comments and the handoff state that the reading line is each section's own
`scroll-margin-top`. The implementation reads `scrollMarginTop` from the FIRST
visible section and applies that single line to the whole scan, so sections with
a different computed `scroll-margin-top` are judged against a line that is not
theirs.

Preferred correction: evaluate each visible section against that section's own
`scroll-margin-top`. Alternatively, encode a single global reading line
explicitly and change the comments and tests to match. Code and claimed algorithm
must not disagree.

### Phase 6 — NARROWLY REOPENED

The existing browser suite is accepted and must stay green; no existing browser
acceptance may be removed or weakened.

Reopened because browser acceptance lacks the adversarial counterexamples above:

- Owner identity: substring-colliding owners; a canonical drill-down that
  excludes the collider; fuzzy search that still discovers both.
- Aggregate failure: the aggregate request failing independently of the roster,
  on first load AND on refresh.
- Denominator: an inconsistent aggregate rendering an explicit remainder rather
  than a fake 100 percent.
- `PageToc`: sections whose computed `scroll-margin-top` values differ.

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

### Blocker A — canonical Owner identity vs fuzzy owner search

Detailed in Phase 5 item 1. Acceptance must prove, with at least the colliding
pair `team-a` / `team-a-platform`:

1. each canonical Owner detail contains only its own estate;
2. the canonical `team-a` service drill-down excludes services owned only by
   `team-a-platform`;
3. a fuzzy search for `team-a` may still discover both;
4. ranking counts still equal their canonical destinations;
5. attention links from one Owner do not broaden to a substring-colliding Owner;
6. the same-team / different-structured-owner case is decided explicitly, either
   as an intentional structured distinction or as a documented and tested
   normalization rule.

### Blocker B — Owners aggregate state is silent on failure

Detailed in Phase 5 item 2. Acceptance must prove, with the aggregate request
failing INDEPENDENTLY of the roster request:

1. first-load aggregate failure is visible and does not silently omit the
   ownership picture;
2. refresh failure over a previously loaded aggregate is visibly reported;
3. the Owner roster remains usable in both cases.

### Blocker C — Owners ownership coverage lacks the authoritative denominator

Detailed in Phase 5 item 3. Acceptance must prove that an aggregate reporting
`services = 10` with buckets summing to 8 renders the 2-service remainder as
`Unclassified`, through the shared `DistributionBar` and with no page-local
special case.

### Blocker D — PageToc reading line is not per-section

Detailed in Phase 5 item 4. Acceptance must prove the chosen rule with at least
two visible sections whose computed `scroll-margin-top` values differ, in a unit
test and in the browser.

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

Reviewed at exact HEAD `0a362da6`.

Review-thread and CodeQL status must be re-established at the exact final SHA of
the next pass, AFTER the final generated UI bundle is committed, and reported as
three separate counts: unresolved authored threads; unresolved generated/minified
vendor-bundle threads; threads resolved but outdated.

The Security workflow's own status is a different claim from CodeQL alert
attribution. Do not describe CodeQL alert provenance as independently established
without inspecting the underlying alert records and their base/head evidence.

The previous Claude handoff for `0a362da6` had reported the local gate matrix
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

1. separate canonical Owner identity from fuzzy owner search across every
   owner-scoped Product path, writing the substring-collision counterexamples
   FIRST, and decide the same-team / different-structured-owner case explicitly;
2. give the Owners aggregate explicit loading / ready / unavailable / stale state
   without destroying the roster;
3. draw ownership coverage against the authoritative service count so a wire
   inconsistency shows as an explicit remainder;
4. make `PageToc` evaluate each visible section against its own reading line, or
   state a single global line and correct the comments and tests;
5. add browser acceptance for all four counterexamples without weakening the
   existing suite;
6. run the full verification matrix, rebuild the UI bundle cold, and audit the
   review threads AFTER the final bundle commit;
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
