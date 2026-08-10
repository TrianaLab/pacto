# Operational Graph dashboard redesign plan

Status: in progress on branch `feat/operational-graph-fleet` (PR #291, draft).

This is the durable, cross-session plan and progress ledger for the operational
graph dashboard product redesign. It records the target product model, the API
and DTO design, the routes and information architecture, the component plan, the
migration sequence, the requirement-to-test mapping, the architectural
decisions, and the completed and pending items. It is not a substitute for
implementation; it exists so the work can continue across fresh sessions without
losing decisions or repeating discovery.

Authored content in this repository must never contain Unicode code point
U+00A7. Use ordinary wording ("requirement 3", "review item 3"). This rule has
two enforcement tiers (requirement 24), and the distinction is load-bearing:

- ACTIVE / BLOCKING: a CI gate scans the authored source and content files and the
  committed generated docs, and fails the build on any U+00A7. This is the tier
  that runs on this PR.
- BLOCKED: the same gate can also scan the commit messages in base..HEAD and the PR
  title and body (its `--commits` mode), but wiring that into blocking CI would fail
  on pre-existing history that already contains the character. Cleaning that history
  would require an explicitly-authorized destructive Git history rewrite, which does
  not exist, so historical commit-message and PR-metadata enforcement stays deferred
  (see section 8 item 9). No prompt in any session has authorized a history rewrite.

## 0. Branch state and synchronized base

The product-foundation rework session (this ledger's phase "product API
hardening") started from and synchronized the branch as follows:

- Starting HEAD: `bc96e3af` (the reviewed HEAD of PR #291).
- Merge-base with main before sync: `ae7273fa`.
- Synchronized base (current `main` tip merged in): `eb1482ff` (five dependabot
  action bumps; the only conflict was `azure/setup-helm` v4 to v5.0.1 in
  `docs-check.yml`, resolved by keeping the mermaid gate and taking the bump).
- Integration strategy: merge (the repo integrates every PR as a merge commit),
  so all branch commits and their content are preserved rather than rebased.
- Post-merge HEAD: `a08b1e82` (advances as foundation commits land).

The product-API hardening session (this ledger's completion of phase 1 items
1-8 plus the U+00A7 boundary) ran as follows:

- Starting HEAD: `8cae98da` (the reviewed HEAD of PR #291).
- `main` had NOT moved (its tip was still the synchronized base `eb1482ff`, which
  equals the merge-base), so no re-sync was needed.
- The session added the route-neutral fleet + typed detail + bounds + expansions
  + impact content-identity backend, the dashboard product transport, the typed
  frontend client + OpenAPI drift gate, and the architecture route-neutral
  invariant, keeping 100% coverage and the PR in draft. No Git history was
  rewritten; the U+00A7 commit-history CI enforcement remains BLOCKED (section 8
  item 9).

The product-API counterexample-closing session (this ledger's re-audit of phase
1 items 4, 7 and 8) ran as follows:

- Starting HEAD: `9e605ab5` (the reviewed HEAD of PR #291).
- `main` had NOT moved (its tip was still the synchronized base `eb1482ff`, which
  equals the merge-base), so no re-sync was needed.
- An independent review found that items 4, 7 and 8 had been marked DONE while
  concrete counterexamples still held, so they were re-opened to IN PROGRESS
  (recorded inline below), closed with new adversarial tests, then re-marked
  DONE only after those tests passed. No Git history was rewritten; the U+00A7
  commit-history CI enforcement stays BLOCKED (section 8 item 9). The PR stays
  draft and its body still describes the earlier dashboard redesign; PR-body
  finalization is a later documentation task (phase 14), not this session.

The generated-SDK + residual-boundedness session reversed the in-Go structural
drift gate in favor of a generated TypeScript SDK (ADR-6) and closed several
product-response counterexamples. Its starting HEAD was `b9d1962c`; `main` had not
moved from the synchronized base `eb1482ff`, so no re-sync was needed. Integration
remains merge (branch content preserved).

The final narrow Phase-1 correction pass (this ledger's current session) ran as
follows:

- Starting HEAD: `f19c531e` (the reviewed HEAD of PR #291).
- `main` had NOT moved (its tip was still the synchronized base `eb1482ff`, which
  equals the merge-base), so no re-sync was needed. Integration remains merge.
- An independent review of `f19c531e` found concrete residual counterexamples that
  showed phase-1 items 4, 7 and 8 were still not truthfully closed. They were
  re-opened to IN PROGRESS, fixed with adversarial tests, then re-closed only after
  those tests passed. No Git history was rewritten; the U+00A7 commit-history CI
  enforcement stays BLOCKED (section 8 item 9). The PR stays draft; PR-body
  finalization is phase 14.

The residual counterexamples this pass closed (final state in section 8):

1. Static transport (item 8): the static seam matched fixtures by PATHNAME only,
   ignoring method, query and body, and returned HTTP 200 + null for any
   non-fixtured route (a legacy call could accept the null as a real answer). It is
   now a request-semantic matcher (method + normalized query + body, order
   independent) and an unfixtured operation fails honestly with a 501 the facade
   turns into an ApiError; the offline single-service `pacto doc` export ships
   explicit fixtures (including the services list and cross-references) rather than
   relying on a universal null fallback.
2. Facade request shapes (item 8): the facade manually duplicated the
   fleetEntities / fleetNeighborhood / fleetAttention / fleetImpactByIdentity
   request shapes, so a new optional wire parameter could be silently dropped
   forever. They are now DERIVED from the generated operations; only the
   array-valued `kinds`/`views` are an explicit ergonomic refinement. Compile-time
   tests prove the relationship.
3. Legacy response types (item 8): every legacy facade method erased its generated
   response type as `Promise<unknown>`. Each now derives its response from the
   generated `paths`; a compile-time test proves NO dashboard operation returns
   `unknown`.
4. Entity-detail narrowing (item 8): `api.fleetEntityDetail` returned the broad
   generated `ProductEntityDetail` and the type guards asserted no runtime
   invariant. A facade-level `narrowEntityDetail` now validates exactly-one-payload-
   matching-kind (and no contradictory payload) and returns `NarrowedEntityDetail`,
   throwing a typed `ApiContractError` on violation.
5. Finite-enum ingestion (item 4): `fleet.Source` is a public extension seam, so a
   custom source could store a non-canonical `Compliance`, finding `Severity` or
   source `Status` that the generated OpenAPI enums declare impossible. `Build` now
   canonicalizes every finite-value field at INGESTION (conservatively, to
   Unknown/partial), keeps the usable record, and surfaces a `SOURCE_RECORD_INVALID`
   limitation, so invalid extension data can never escape as an out-of-schema enum.
6. Target-link identity (item 7): the model now tracks TWO orthogonal identity
   dimensions instead of conflating them. Revision-match certainty (`matchRevision`
   -> a target's `LinkState`: exact / inferred / ambiguous / unresolved) answers "how
   confidently do we know which revision this target is running". Content
   retrievability (`ClassifyContentIdentity` -> `RevisionIdentity.Retrievable` +
   `IdentityClass`: exact / missing-digest / mutable / no-ref / local / malformed /
   digest-mismatch) answers "can Pacto retrieve exactly this content". `matchRevision`
   reuses the retrievability classifier only to derive the effective content digest
   and to reject a self-contradictory identity, so the two never DISAGREE dishonestly
   -- but they may differ HONESTLY: a target with a trusted digest and no canonical
   ref is an EXACT match whose content is NOT retrievable. A digest/ref DISAGREEMENT,
   by contrast, is a genuine inconsistency: never an exact link, and it surfaces a
   limitation.
7. RuntimePreview work (item 4): `RuntimePreview` bounded its OUTPUT but not its
   WORK — `keysBounded` scanned the whole observed-runtime map at QUERY time
   (O(map width)). The bounded projection is now computed ONCE at Build (the single
   documented unbounded-source pass) and stored on the target, so the product query
   is O(fixed bound); the raw map is not retained on the snapshot.
8. ProductFinding conversion (item 4): `productFinding`/`findingsPreview`/attributed
   findings converted EVERY (possibly extension-supplied, unbounded) evidence ref
   and finding before truncating to a bounded preview. They now convert only the
   emitted prefix while reporting the truthful total, proven by allocation-bounded
   adversarial tests.

The Phase-3 session (product lists, rich entity pages and attention completion) ran
as follows:

- Starting HEAD: `81f76894` (the reviewed HEAD of PR #291).
- Synchronized base: `eb1482ff` (current `main` tip). `main` had NOT moved from that
  base (it equals the merge-base and is an ancestor of HEAD), so no re-sync was
  needed. Integration remains merge (branch content preserved).
- The session began with a small preflight closing six Phase-2 product
  counterexamples (empty-fleet vs all-clear, real attention pagination, the
  `/fleet/services` canonical route, the entity-search stale-request race, the
  `navigate('fleet')` router-semantic trap, and the visible global-search
  affordance), then continued directly into Phase 3. No Git history was rewritten;
  the U+00A7 commit-history CI enforcement stays BLOCKED (section 8 item 9). The PR
  stays draft; PR-body finalization is phase 14.

The Phase-3 closure + Phase-4 session (this ledger's current session) ran as follows:

- Starting HEAD: `6f7cb1a3` (the reviewed HEAD of PR #291).
- Synchronized base: `eb1482ff` (current `main` tip). `main` had NOT moved from that
  base (it equals the merge-base and is an ancestor of HEAD), so no re-sync was
  needed. Integration remains merge (branch content preserved).
- An independent review of `6f7cb1a3` found five concrete correctness gaps that
  showed Phase 3 was not truthfully closed: (A) the Impact workspace still loaded the
  raw `FleetSnapshot` and called the legacy `GET /api/fleet/impact` instead of the
  product `fleetImpactByIdentity` POST, and Compare launched Impact from a service
  NAME rather than a canonical `ServiceKey`; (B) `PreviewSection` collapsed an
  UNKNOWN exact total into `total ?? count`/`total ?? scanned`, rendering "X of X"
  for a truncated preview whose true total is unknown; (C) `siblingRevisions` ordered
  Previous/Next by lexical `RevisionKey` (digest order), not revision chronology; (D)
  `snapshotKnowledge` did not model the backend `empty` completeness and
  filtered-empty suppressed the incompleteness caveat; (E) the four product-list
  views issued duplicate initial loads and had no stale-response protection. Phase 3
  was REOPENED to IN PROGRESS while these were closed with adversarial tests, then
  re-marked COMPLETE only after those tests passed. No Git history was rewritten; the
  U+00A7 commit-history CI enforcement stays BLOCKED (section 8 item 9). The PR stays
  draft; PR-body finalization is phase 14. The session then continued DIRECTLY into
  Phase 4 (search-first Operational Graph) in the same pass.

The second-correction-pass session (this ledger's current session) ran as follows:

- Starting HEAD: `2efeb9ef` (the independently reviewed HEAD of PR #291).
- Synchronized base: `a56b69e3` (`main` tip for that session). This entry originally
  recorded `eb1482ff`, the base of the EARLIER sessions above; that was wrong for
  this one. `main` had advanced to `a56b69e3` and the branch had already been
  synchronized to it, so `a56b69e3` is the merge-base and an ancestor of HEAD and no
  re-sync was needed. Integration remains merge (branch content preserved). The
  earlier entries' `eb1482ff` is left alone: it was genuinely their base.
- Exact-SHA CI was green at `2efeb9ef` and a second real-user review still found five
  concrete regressions the acceptance suite did not cover -- two of them capabilities
  the previous dashboard shipped, both recorded in this file as deliberate scope. The
  Product-UI / information-parity / interaction / browser-acceptance items were
  REOPENED narrowly and re-closed; the proven engine, identity, fleet-query,
  graph-projection, boundedness and evidence semantics were not reopened. See "Second
  correction pass" below. No Git history was rewritten, rebased or force-pushed; the
  U+00A7 commit-history CI enforcement stays BLOCKED (section 8 item 9). The PR stays
  draft; PR-body finalization is phase 14. Phase 7 was NOT started.

The third-correction-pass session (this ledger's current session) ran as follows:

- Starting HEAD: `13810112` (the independently reviewed HEAD of PR #291).
- Synchronized base: `a56b69e3` (current `main` tip). `main` had NOT moved from that
  base (it equals the merge-base and is an ancestor of HEAD), so no re-sync was
  needed. Integration remains merge (branch content preserved).
- Exact-SHA CI was green at `13810112` and an invariant review still found four
  counterexamples the suite could not see -- three of them cases where the product
  answered CONFIDENTLY and wrongly rather than failing. Phases 3, 5 and 6 were
  REOPENED narrowly for exactly those, and re-closed. The proven engine, identity,
  fleet-query, graph-projection, boundedness and evidence semantics were not
  otherwise reopened. See "Third correction pass" below. No Git history was
  rewritten, rebased or force-pushed; the U+00A7 commit-history CI enforcement stays
  BLOCKED (section 8 item 9). The PR stays draft; PR-body finalization is phase 14.
  Phase 7 was NOT started.

The fourth-correction-pass session (this ledger's current session) ran as follows:

- Starting HEAD: `759845ca` (the independently reviewed HEAD of PR #291).
- Synchronized base: `a56b69e3` (current `main` tip). `main` had NOT moved from that
  base (it equals the merge-base and is an ancestor of HEAD), so no re-sync was
  needed. Integration remains merge (branch content preserved).
- Two findings reopened work, both NARROW:
  1. one reference-identity counterexample. `pacto.lock` holds the TRANSITIVE
     reference closure, so it routinely carries several entries sharing a
     (kind, name). Looking a reference up by kind and name returned an
     authoritative digest FOR A DIFFERENT REFERENCE OCCURRENCE, which renders as a
     confident canonical Product link the contract never declared. Phase 3 reopened
     for exactly this.
  2. a presentation-system defect. Product components reference GLOBAL design
     tokens that were never declared, so their declarations are invalid at computed
     value time and the browser silently falls back -- a section title ends up
     outranking the page title it sits under. Phases 5 and 6 reopened for the token
     system, the typography hierarchy, information density / progressive disclosure
     and consistent visual nesting, plus browser acceptance of those.
- Phase 4 stays COMPLETE and is not reopened. Document immutability, stale-while-
  revalidate, scroll restoration and graph semantics stay CLOSED -- no new
  counterexample was found for any of them. The Product design freeze remains in
  force: this pass corrects presentation and one identity bug, and does not
  redesign the product model, its ontology or its IA.
- No Git history was rewritten, rebased or force-pushed; the U+00A7 commit-history
  CI enforcement stays BLOCKED (section 8 item 9). The PR stays draft; PR-body
  finalization is phase 14. Phase 7 was NOT started.

## 0a. Current status (authoritative)

This is the single authoritative status. Any older phase heading below is
historical narrative; where it conflicts with this section, this section wins.

**One canonical program sequence.** Phase numbers mean exactly what section 5
says they mean, and nothing else. Section 5 is the numbering; this section is the
status of that numbering. In particular: **Phase 6 is WASM browser acceptance**,
as it always was. Earlier sessions used "Phase 6" as a working label for the
product-coherence / novice-usability correction; that was a second meaning for a
number already taken, and it is retired here. That work is now recorded under its
own name — the **product-coherence correction** — because it was not a new phase
at all: it re-opened and re-closed deliverables belonging to phases 2, 3 and 4
(IA, entity pages, graph) after a real user reported the app "still feels like
several generations of Pacto UI stitched together". Headings below that read
"Phase 6 ... coherence" describe that correction, not whole-program Phase 6.

- Phase 1 (product API hardening): COMPLETE.
- Phase 2 (frontend IA and routing): COMPLETE.
- Phase 3 (Overview, Services, Attention, rich entity pages): COMPLETE (re-closed).
  Reopened narrowly by the third correction pass over two claims the pages made and
  could not support: a document body read live from a mutable filesystem under an
  immutable revision identity, and a config/policy reference link resolved from a
  plausible name. Both are now evidence-only. Re-closed on the adversarial acceptance
  in `pkg/fleet/document_immutable_test.go` (11 tests) and
  `pkg/fleet/refresolution_test.go` (14 tests). Nothing else in Phase 3 was reopened.
  REOPENED again by the fourth correction pass, narrowly, for reference-OCCURRENCE
  identity: `refresolution_test.go` established that a destination may only come
  from an authoritative immutable identity, but not that the identity used is the
  one recorded for THIS declared reference. RE-CLOSED: `lock.Reference` now carries
  `From`, the closure path of the declaring contract, so From+Kind+Name names one
  occurrence; `RootReference` answers only for the root's own entries and never
  falls back to a transitive namesake; the closure builder emits one entry per
  occurrence and dedups the walk on RESOLVED bundle identity, so two contracts
  declaring `./config` are two entries and cycles still terminate; `lockVersion`
  1 -> 2, with v1 locks degrading to unresolved with a stated reason rather than
  being reinterpreted. Acceptance: `pkg/fleet/refoccurrence_test.go` (8) and
  `internal/app/lock_occurrence_test.go` (6), plus the `pkg/lock` version and
  ordering tests; the whole engine suite is green at 100.0% total coverage. See
  "Fourth correction pass: a reference is an occurrence, not a label" below. The
  final gate is final-SHA CI. Nothing else in Phase 3 was reopened.
- Phase 4 (search-first Operational Graph + full dashboard migration): COMPLETE
  (re-closed). An independent review of HEAD `8a2f7910` reopened Phase 4 over six concrete
  gaps (the most important user-visible: the graph tab read as NO GRAPH). All six are now
  closed with tests that fail before the fix and pass after; the final gate is final-SHA CI:
  1. the silent HEADLESS `renderGraph()` fallback is gone. It feature-detects a real 2D
     canvas (jsdom -> headless on purpose; a real browser MUST paint) and a visual failure
     is a typed GraphRenderError the view surfaces as an explicit render-error state, never
     a silently empty canvas.
  2. a real visual-canvas browser gate (graph-visual.spec.ts) proves a NON-HEADLESS
     renderer painted the focused payments-service topology: real canvas, nonzero CSS +
     backing store, expected node/edge counts, >=2 rendered node boxes, >=1 rendered edge,
     non-blank pixels, no console/page errors -- while discovery stays zero-topology.
  3. `NeighborhoodEdge.Provenance` now declares the finite enum `declared,observed,
     declared+observed` (canonical `ProvenanceDeclaredObserved`), matching what
     `edgeProvenance` emits; a field-specific OpenAPI enum test + a combined-case transport
     test + a regenerated deterministic SDK enforce it.
  4. `projectEdgeForViews` now clears the nested `DeclaredClaim.Reconciliation` under
     Expected-only and Expected+Observed-without-Differences, proven by a nested-items test.
  5. the graph adapter + canonicalizers derive their wire types from the generated
     ProductNeighborhood (indexed access + Pick); a compile-time typetest guards against a
     hand-mirror creeping back.
  6. the legacy `#/services/:name/versions/:version` bookmark migrates to a canonical
     Product Revision (resolve service -> revisions scoped -> matching RevisionKey via an
     explicit, backend-authoritative revision version), disambiguating / not-found honestly.
  The discovery affordance was also strengthened (an unmistakable "graph renders after you
  focus" placeholder, no whole-fleet auto-render). The earlier `540cf692` / `973daa14`
  blockers remain closed. The visual-acceptance blocker is cleared.
- Phase 5 (responsive + accessible interaction: keyboard graph navigation, mobile
  layout, formal WCAG): COMPLETE. DONE: the real
  WCAG contrast gate -- axe no longer blanket-disables color-contrast; the design tokens
  were measured in both themes and deepened so every rendered text pair clears AA 4.5:1
  (new --c-on-accent, deepened light accent/ok/warn, removed the empty-state opacity fade
  that dipped muted text mid-animation), with light-theme audits added and the default
  audits now genuinely testing dark (headless prefers light); the graph drawer's Escape
  moved off the complementary landmark; deeper 320/375px interactive-state responsive
  acceptance (graph drawers + navigator, attention advanced filters, mobile nav, populated
  impact). The keyboard graph model (semantic Relationships navigator: discover/focus,
  traverse nodes + edges, inspect, Escape restores focus, open full detail, change
  perspective/views/direction) is covered by keyboard.spec.ts. The last open item -- a
  full explicit heading/landmark sweep across every canonical route -- is now done and
  gated by `e2e/headings.spec.ts`; it found and fixed a per-route page title, a skipped
  heading level in the shared empty state, a missing banner landmark next to two
  navigation landmarks, and an entity page with no heading while loading or not found.
  See "Phase 5 closure" below. The "retained specialized Readiness/Compare" this bullet
  used to name no longer exist on a Fleet host -- see the product-coherence correction
  below. Re-closed after the third correction pass reopened it narrowly for scroll
  restoration: a place belongs to a HISTORY ENTRY, not to a URL, so two entries showing
  the same page keep their own places. `lib/scrollRestore.test.ts` (16 tests) and the
  browser counterexample in `place.spec.ts`.
  REOPENED again by the fourth correction pass, narrowly, for the PRESENTATION
  SYSTEM: design-token integrity (a Product component may not reference a global
  token nobody declares), typography hierarchy (visual role, not HTML tag, decides
  size and weight), information density and progressive disclosure, and consistent
  visual nesting. RE-CLOSED against those criteria: zero undeclared global tokens
  on any Product surface (guarded, with a positive and a negative fixture so the
  scope is provably neither vacuous nor over-broad); nine `--role-*` tokens and
  `.t-*` classes with no component setting its own heading size; the same
  disclosure grammar on every dense entity page with nothing removed and every
  closed section labelled with its count; heading semantics and the axe /
  heading-landmark sweeps still green; and the nine-question cognitive walkthrough
  run against BEFORE/AFTER captures of the built WASM Product in a real browser,
  which itself found one further defect (a verdict badge breaking mid-word) that
  was root-caused and fixed. Two items are recorded there as design debt rather
  than closed: two list pages carry summary charts as subsection-role h2s with no
  enclosing section title (restructuring that is IA work, and the design freeze is
  in force), and the "Confidence" column header wraps its help button at 1440px.
  See "Fourth correction pass: the presentation system" below. The final gate is
  final-SHA CI. The measured contrast gate, the keyboard graph model, the
  responsive acceptance and the heading/landmark sweep were NOT reopened.
- Phase 6 (WASM browser acceptance): COMPLETE (re-closed). It had been marked COMPLETE
  at `c5fdc1c4` with 133 Playwright tests across 14 specs and one `test.fixme` -- and
  that status was NOT truthful, because the `fixme` covered a capability the previous
  dashboard really had (rendered doc bodies) and because three further regressions had
  no acceptance at all. A second independent real-user review of `2efeb9ef` found them.
  Phase 6 was REOPENED, narrowly, together with the information-parity and interaction
  ledger items it depends on, and is re-closed here: 158 Playwright tests across 17
  specs, desktop plus Pixel-5, and NO `test.fixme` covering a required existing
  capability. See "Second correction pass" below for the five defects, their root causes
  and the specs that now hold them. The one remaining `test.skip` is a data guard in
  `headings.spec.ts` (skips when the demo publishes no legacy service to open), not a
  deferred capability. Re-closed AGAIN after the third correction pass, which reopened
  it for the two interaction counterexamples no browser test could have caught without
  a way to HOLD a request in flight: 167 Playwright tests across 18 specs (desktop plus
  Pixel-5), the new spec being `swr.spec.ts` (8 delayed-network tests) plus the
  repeated-URL history counterexample added to `place.spec.ts`. Still exactly one
  `test.skip` and no `test.fixme`. The final gate is final-SHA CI. Phase 7 has NOT been
  started.
  REOPENED again by the fourth correction pass, narrowly, to accept the
  presentation-system correction in a real browser: computed-style typography
  acceptance (page title dominates its section titles; two components in the same
  visual section role compute the same size and weight even when one is an h2 and
  the other an h3; a subsection is smaller than its parent section), desktop and
  mobile, plus disclosure semantics and accessibility. RE-CLOSED: 171 Playwright
  tests across 19 specs (desktop plus Pixel-5) against the built WASM demo, the new
  spec being `typography.spec.ts` -- eleven canonical routes measured in Chromium,
  sharing `typographyChecks.ts` with the mobile ramp block in `mobile.spec.ts`. All
  previous browser acceptance stays green, and there is still exactly one
  `test.skip` (the `headings.spec.ts` data guard) and no `test.fixme`, so no
  required migrated capability is deferred. The final gate is final-SHA CI. Phase 7
  stays NOT started.
- Product-coherence correction (cross-phase; NOT whole-program Phase 6, which is WASM
  browser acceptance): COMPLETE. A real user reported the app
  "still feels like several generations of Pacto UI stitched together". This phase was a
  first-time-user product review of the BUILT WASM demo in a real browser (30 BEFORE
  screens captured with a cognitive walkthrough), followed by implementation, followed by
  the same capture again. What changed, and why:
  1. ONE vocabulary at the surface. `target` is an **Operational target** everywhere a
     user reads it, never a "Deployment" (which now means only the Kubernetes kind).
     `source` is a **Data source**, never a collector. `EntityKind=target`, `TargetKey`,
     the `/fleet/...` routes, the wire field names and the Kubernetes kind name are
     UNCHANGED -- this was a display-string migration, not a model rename.
  2. "Fleet" left the user-facing surface. It stays as the internal name it always was:
     `pkg/fleet`, `/fleet/...` routes, `pacto fleet`, `pacto_fleet_*`. The breadcrumb root
     is now "Overview", not "Fleet".
  3. FOUR primary workflows, teaching one order -- state -> inventory -> relationships ->
     change: **Overview - Services - Operational Graph - Change analysis**. Owners and
     Data sources remain first-class destinations, reached from where they are relevant
     rather than from a nav bar that made a novice choose between seven peers.
  4. Readiness is a DIMENSION, not a fifth destination. It is the `readiness` attention
     category and a section on a Revision -- the two definitions that already existed. No
     third definition was introduced, and `#/readiness` canonicalizes into the attention
     view rather than mounting the legacy screen (which, on a Fleet host, rendered "No
     services" -- it had been dead for some time).
  5. Compare and Impact are ONE workspace: **Change analysis** (`#/fleet/changes/:svc`),
     RevisionKey-based end to end, keeping the full field-level semantic diff. The primary
     CTA still reads "Compare revisions" because that is the action. `#/diff` and
     `#/fleet/impact/:svc` canonicalize into it.
  6. Expert ontology moved one disclosure away without being lost: the canonical key is
     behind an "Identifier" disclosure, the exact/inferred/ambiguous/unresolved taxonomy
     behind a plain-language headline ("We know exactly which revision is running on N of
     M operational targets"), the snapshot id behind a "Current data / Older snapshot"
     chip's tooltip.
  7. ONE visual language. The AFTER screens exposed the literal form of "several
     generations of UI": one control (`<details>`) carried five unrelated designs across
     five screens, `.btn` had been re-implemented four times under four private names, and
     two status pickers each hand-listed four of the seven wire statuses. All three are now
     single shared idioms -- see "second pass: visual coherence" below.
  Two REAL bugs were found by walking the product rather than reading it: the primary-nav
  "Services" link was a silent no-op while capabilities were still unprobed (its `'#/'`
  fallback canonicalized straight back to the Overview on a Fleet host), and a shared
  `?old=&new=` Change-analysis link restored the FORM but not the ANSWER, so "shareable
  URL" was only half true. Both are fixed at the source with unit tests.
  The acceptance gate is `e2e/novice-journeys.spec.ts`: twelve first-time-user journeys
  (J1-J12) run in a real browser against the built WASM demo, each asserting routes,
  conceptual labels, canonical identity, workflow continuity and -- via a shared
  `LEGACY_MARKERS` locator -- the ABSENCE of any legacy screen. J12 walks every legacy
  bookmark and proves it canonicalizes into the product IA without mounting an older UI.
  Non-Fleet hosts (the offline `pacto doc` export) deliberately keep the legacy screens:
  they are the only UI on a host that has no Product API.

Reproduction of record (this session, built WASM demo in real Chromium): the
DISCOVERY route `#/fleet/graph` correctly renders ZERO Cytoscape topology
(search-first by design). The FOCUSED route
`#/fleet/graph/service/payments-service` DID paint a visible topology on the
reviewed build: three non-headless Cytoscape canvas layers at 742x458 (CSS and
backing store), the node layer non-blank, seven nodes and eight edges, no console
or page errors. A blank FOCUSED canvas was therefore NOT reproduced on this build.
The user's "no graph" is best explained by (a) the search-first DISCOVERY route,
whose affordance is too weak, so the graph tab reads as an empty page (section 4),
and (b) the latent silent-headless fallback (gap 1), which would hide any real
renderer failure. Both are now fixed: the discovery affordance is explicit and the
silent fallback is replaced by a typed render error, and a real non-headless canvas
gate (graph-visual.spec.ts) now proves the focused topology actually paints.

The projection / materialized-storage work from the earlier evidence-store review
(ADR-5) is resolved and is NOT reopened. The U+00A7 enforcement tiers are as stated
in the header: the authored source/content gate is ACTIVE and blocking; historical
commit-message and PR-metadata enforcement is BLOCKED on explicit history-rewrite
authorization, which does not exist this session. No Git history is rewritten,
rebased or force-pushed this session.

## 0b. Product vocabulary and user mental model (design contract)

This section is the design contract for user-facing wording. It is NOT a glossary
to render on a page. Every label, subtitle, empty state, tooltip, breadcrumb, nav
item, test assertion and doc sentence in the product UI must agree with it. The
reader it is written for is a platform engineer who understands services and
Kubernetes but has never heard of Pacto.

The mental model the primary navigation teaches, in order: **state, then
inventory, then relationships, then change.**

| Concept | User-facing name | One-line meaning shown to a first-time user | Internal identifier (unchanged) |
|---|---|---|---|
| Logical software capability | **Service** | A service Pacto knows about, identified by domain plus name. | `EntityKind=service`, `ServiceKey` |
| Immutable contract version | **Revision** | One immutable published version of a service contract. | `EntityKind=revision`, `RevisionKey` |
| A place a revision runs | **Operational target** | A concrete place a revision is running, such as a Kubernetes workload. | `EntityKind=target`, `TargetKey` |
| Ingestion seam | **Data source** | Where Pacto read this from: a registry, local files, a cluster or an evidence store. | `EntityKind=source`, `SourceID` |
| Observer of a real system | **Collector** | A component that watches a real environment and reports evidence. Not every data source is a collector. | evidence producers |
| Observed facts | **Evidence** | What was actually observed, with a time and a reporter. | `EvidenceSet` |
| Contract-vs-evidence verdict | **Compliance** | Whether what is running matches what the contract promised. | `Evaluate(Contract, EvidenceSet)` |
| Authored preparedness gate | **Readiness** | A self-assessment checklist authored in the contract, scored against a gate. Declared, not observed. | `pkg/readiness` |
| Typed conclusion | **Finding** | One specific problem, with a code, a severity and a category. | `finding.Finding` |
| Snapshot honesty | **Knowledge** | How complete this answer is: whether a source was unavailable, partial or stale. | `Completeness`, `Limitation` |
| Relationship graph | **Operational graph** | Which services depend on which, expected versus actually observed. | `FleetSnapshot`, `Neighborhood` |
| Change plus consequence | **Change analysis** | What changed between two revisions, and what that change can affect. | `pkg/diff`, `pkg/impact` |

Distinctions a first-time user must be able to learn from the UI alone:

- **Readiness is not compliance.** Readiness is authored in the contract and asks
  "is this contract prepared". Compliance compares a contract against observed
  evidence and asks "does what is running match what was promised". A revision can
  be fully ready and still non-compliant, and vice versa.
- **A data source is not a collector.** A data source is the seam Pacto ingests
  through. A collector is a component that observes a real running environment and
  emits evidence. An OCI registry, a local directory and the on-disk cache are data
  sources and are NOT collectors. Never rename `source` to `collector` anywhere.
- **An operational target is not a deployment Pacto performs.** Pacto observes
  targets; it never deploys. The generic identity is `scope/kind/name`, which
  happens to be Kubernetes-shaped today but is not Kubernetes-only. This is why the
  UI says "Operational target" and not "Deployment": "Deployment" invited the reading
  that Pacto is a deployment engine, and it also collides with the Kubernetes kind.
- **Expected is not observed.** A declared dependency is a claim in a contract. An
  observed relationship is traffic somebody saw. The reconciliation of the two is a
  Difference, and "we could not observe it" is never reported as "it is not there".
- **An unavailable source is not an empty result.** Incomplete knowledge is stated
  as incomplete knowledge, never rendered as a clean, healthy zero.

Words that are INTERNAL and must not appear in first-time-user copy:

- **"Fleet"** — the internal name of the immutable read model (`FleetSnapshot`) and
  its pure query layer (`FleetQuery`). Routes (`#/fleet/...`), Go packages, API
  paths, DTO names and test identifiers keep it. Product copy says "Operational
  graph", "services", "everything Pacto knows" or the concrete noun. There is no
  `kind: Fleet` in the contract language, so a user has no way to learn the word.
- **"Target"** as a bare user-facing noun (the entity is an "operational target").
- **"Snapshot ID", "canonical key", "identity class", "provenance"** — retained at
  full precision, but demoted below the fold or into progressive disclosure rather
  than presented in the first screenful.

Primary navigation is exactly four workflows, which is the mental model above:

| Nav item | The user question it answers |
|---|---|
| **Overview** | What is the state of everything, and what needs me? |
| **Services** | What exists, and what is the situation of this one? |
| **Operational Graph** | What is connected to what, and what is expected versus observed? |
| **Change analysis** | What changed between two revisions, and what can that affect? |

Owners, Data sources, Needs attention and Readiness are DIMENSIONS of those four,
not peer destinations. They are reachable from the Overview, from entity pages and
from the command palette, and they keep their own routes; they are not top-level
tabs. Desktop nav, mobile nav and the command palette all agree on this ordering.

## 0c. Old-detail to Product-detail parity matrix (requirement 1)

The legacy dashboard had ONE page — `views/ServiceDetailView.svelte` plus
`src/sections/**` — that answered every question about a service. The Product
entity model deliberately splits that page across three entities, because the old
page conflated three different things under one name (a logical service, an
immutable contract revision, and a running instance). A split is not a licence to
lose information, so every capability of the old page is classified below into
exactly one destination:

- **SERVICE** — a property of the logical service across all its revisions and
  targets.
- **REVISION** — a property of an immutable contract revision (declared).
- **OPERATIONAL TARGET** — a property of a running instance (observed).
- **CHANGE ANALYSIS** — a property of the difference between two revisions.
- **REMOVED BY PACTO V2 MODEL** — the v2 slim contract no longer declares it and
  no runtime source observes it, so there is nothing to render. Rendering it
  would mean inventing it.
- **NON-FLEET COMPATIBILITY ONLY** — retained on the offline `pacto doc` export,
  which has no Product API, and deliberately not migrated.

"In API" = the Product API already carries the fact. "Rendered" = a product page
actually shows it. Both columns are the state AFTER the restoration commit that
accompanies this matrix.

### Header and identity

| Old capability | Destination | In API | Rendered | Action taken |
|---|---|---|---|---|
| Service name / title | SERVICE | yes | yes | entity header + breadcrumbs |
| Contract status badge | SERVICE (aggregate) and OPERATIONAL TARGET (per instance) | yes | yes | the old single badge conflated the two; the service page shows the compliance DISTRIBUTION over its complete target population, the target page shows its own verdict |
| "Definition only" badge (no runtime data) | SERVICE | yes | yes | expressed as the evidence-freshness distribution plus an explicit "nothing running has been observed" empty state — never as a failure |
| Numeric compliance score | REMOVED BY PACTO V2 MODEL | no | no | Compliance 2.0 is four named states, not a percentage. A score averaged a confirmed contradiction with a cannot-observe, which is the exact conflation the 4-state model exists to prevent |
| Error / warning count badges | SERVICE | yes | yes | severity distribution + the attributed findings list |
| `checksSummary` passed/total | OPERATIONAL TARGET | yes | yes | `coverage.evaluated of coverage.required` on the target page |
| Evaluation coverage | OPERATIONAL TARGET | yes | yes | same row as above |
| Blast radius indicator | CHANGE ANALYSIS | yes | yes | impact analysis over the canonical RevisionKey, with the affected consumers enumerated instead of a bare number |
| Version pill | REVISION | yes | yes | revision header |
| "via cluster" override pill | OPERATIONAL TARGET | yes | yes | the target's own observed identity, kept separate from the declared one |
| Version policy pill (tracking / pinned-tag / pinned-digest) | OPERATIONAL TARGET | yes | yes | superseded by the two-dimension identity model (revision-match certainty + content retrievability), which says strictly more: an exact match to non-retrievable content is now expressible and was not before |
| "N available" update pill + Compare CTA | SERVICE | yes | yes | revisions list is newest-first with "in use" marked; Compare is the Change analysis CTA |
| Source dots | SERVICE and OPERATIONAL TARGET | yes | yes | `sources` preview on the target; source health strip on the Overview |
| Owner link, DRI | SERVICE / REVISION / TARGET | yes | yes | `ownership` on all three, plus ownership-conflict reporting the old page did not have |
| Namespace | OPERATIONAL TARGET | yes | yes | `scope` |
| Resolved / image ref | REVISION and OPERATIONAL TARGET | yes | yes | `identity.resolvedRef` on both |
| Version `<select>` + "Compare versions" | REVISION | yes | yes | the scoped revision inventory (`/fleet/entities/revision?service=`) is a bounded, canonical, paged list; the old `<select>` was an unbounded name-based fetch |
| Reference-only banner | REVISION | yes | yes | `Reference` status |
| "Viewing version X, not current" banner | REVISION | yes | yes | the revision page IS a specific revision; previous/next links carry the chronology |

### Sections

| Old section | Destination | In API | Rendered | Action taken |
|---|---|---|---|---|
| Insights list | SERVICE | yes | yes | replaced by attention + attributed findings, which are backend-derived rather than client-computed |
| Endpoint Probes | OPERATIONAL TARGET | **no** | no | Pacto no longer probes endpoints and no collector reports probe results. Not fabricated. Recorded as a collector gap, not a UI gap |
| `OverviewSection` — operator Conditions | OPERATIONAL TARGET | **no** | no | the k8s collector (`internal/fleetsrc/k8s.go`) does not currently carry operator `status.conditions` into the snapshot. Collector gap, recorded, not fabricated |
| `OverviewSection` — Runtime card (`upgradeStrategy`, `gracefulShutdownSeconds`, `healthPath`, `metricsPath`) | REMOVED BY PACTO V2 MODEL | no | no | the v2 slim contract does not declare these and nothing observes them |
| `OverviewSection` — Scaling card | REMOVED BY PACTO V2 MODEL | no | no | as above |
| `OverviewSection` — Resources | REMOVED BY PACTO V2 MODEL | no | no | as above |
| `OverviewSection` — contract metadata | REVISION | yes (added) | yes (added) | `ContractRevision.Metadata`, bounded ONCE at Build like observed runtime, because the map is author-controlled and arbitrarily wide. Rendered as "Contract metadata" |
| `SourcesPanel` | SERVICE and OPERATIONAL TARGET | yes | yes | `sources` preview + Data sources destination |
| `InterfacesSection` | REVISION | yes | yes | interfaces with their OPERATIONS (name, method, path, summary, mutating), not a count. This is the named regression the parity test guards |
| `CapabilitiesSection` (capabilities + skills) | REVISION | yes | yes | each capability with its binding, or an explicit "no binding declared" |
| `DependenciesSection` — service names | REVISION (declared) and SERVICE (neighbourhood) | yes | yes | both |
| `DependenciesSection` — Ref / Required / Compatibility / Pinned columns | REVISION | yes | **yes (restored)** | the API always sent `NeighborhoodEdge.declaredClaims`; the row dropped them. `RelationshipList` now renders them behind `showClaims`, on outbound edges only |
| `ConfigSection` — schema path | REVISION | yes | yes | |
| `ConfigSection` — effective key/value table | REVISION | yes | yes | bounded `values` preview per configuration scope |
| `ConfigSection` — remote ref NAVIGABLE to the referenced service | REVISION | **yes (added)** | **yes (restored)** | the legacy page made a remote ref clickable by splitting the ref string. The row rendered the ref as inert text, which is a real loss. The fleet builder now resolves each config/policy ref to a canonical `ServiceKey` inside the REFERRING revision's own domain and publishes the verdict as `RefResolution`; the UI links to what the backend resolved and never re-derives a destination from a string or a label. The raw ref stays visible verbatim; an unresolved ref says so with the backend's reason and fabricates no service |
| `PolicySection` | REVISION | yes | yes | name, definition, target, local/remote; remote refs are navigable on the same `RefResolution`, same rule |
| Reverse reference navigation ("who reads my config / policy") | SERVICE | **yes (added)** | **yes (added)** | a reference is not a dependency, so a shared configuration or policy service was reachable FROM its consumers but could never list them back. `ServiceDetailData.ReferencedBy` closes the loop. The old page did not have this at all |
| `ReadinessSection` — score / gate / checks | REVISION | yes | yes | readiness is declared and gated PER REVISION; the service page deliberately does not roll it up, which would invent a third definition |
| `ReadinessSection` — reported readiness | OPERATIONAL TARGET | yes | yes | kept separate from the declared gate, because the two can legitimately disagree |
| `RevisionHistory` (inside readiness) | REVISION | yes | yes | previous/next revision links + the scoped revision inventory |
| `DocsSection` — doc list (title/path) | REVISION | yes | yes | bounded `docs` preview |
| `DocsSection` — rendered doc BODIES (Markdown + Mermaid) | REVISION | **yes (added)** | **yes (restored)** | this row once read NON-FLEET COMPATIBILITY ONLY with a post-freeze follow-up; that classification was wrong and is withdrawn — it recorded a real capability loss as a design boundary. A body is now read LAZILY, one document at a time, by canonical RevisionKey plus a path the revision itself published (`Query.RevisionDocument`, `GET /api/fleet/revisions/document`), so the entity response stays bounded and the doc list stays a preview. The published-path allow-list is the traversal and cross-revision defense; `MaxDocumentBytes` bounds the read and an oversized, unreadable or non-text body is an explicit 422, never empty content. Rendered through the shared `MarkdownView` (sanitized, Mermaid `securityLevel: 'strict'`). `mermaid.spec.ts` is migrated to this path and is no longer `test.fixme` |
| `SbomSection` | REVISION | yes (added) | yes (added) | `SBOMSummary`: format, the COMPLETE package count, and license buckets capped at 20 with the tail folded into `OtherLicensed`, so the buckets always partition the package population. The packages themselves stay in the bundle — a snapshot holds every revision of every service. An unparseable SBOM raises `SBOM_UNREADABLE` rather than rendering as "declares no packages" |
| `ValidationSection` — findings | REVISION | yes | yes | `validation` preview |
| `ValidationSection` — operator conditions | OPERATIONAL TARGET | **no** | no | same collector gap as `OverviewSection` conditions |
| `RuntimeDiffSection` — per-field declared-vs-observed rows | OPERATIONAL TARGET | **no** | no | the snapshot carries observed runtime and declared contract separately, but no collector emits the field-level reconciliation rows. The service-level declared-vs-observed reconciliation IS carried (`difference` per edge) and is rendered. Recorded as a collector gap |
| `ObservedRuntimeSection` | OPERATIONAL TARGET | yes | yes | `observedRuntime` bounded preview, plus `labels` which the old page did not have |
| Sticky "On this page" TOC | (presentation) | n/a | n/a | not information; the product pages are shorter and use headings + landmarks instead |

### Deliberate non-parity

Two things the old page did that the product IA will NOT do, each for a stated
reason rather than by omission:

1. **A version TIMELINE chart.** The snapshot has no publication history:
   `FetchedAt` is when we fetched a revision, not when it was published. Drawing a
   timeline from it would be a fabricated time series.
2. **A single service health score.** See the compliance-score row above.

This list used to have a third entry, "doc bodies on a Fleet host". It did not
belong here: the other two are facts the model genuinely cannot produce without
inventing them, while doc bodies were a capability the previous dashboard shipped
and the product IA dropped. A capability loss recorded as a design boundary is how
a regression survives a review, so it is withdrawn and the capability is restored.
See the `DocsSection` doc-BODIES row above.

### Collector gaps (not UI gaps)

Recorded so they are not mistaken for information loss in the redesign: operator
`status.conditions`, endpoint probe results, and field-level runtime diff rows are
absent because no source in `internal/fleetsrc` collects them into the snapshot,
not because a product page declines to show them. Each belongs to OPERATIONAL
TARGET and would render there the day a collector supplies it.

### Enforcement

`src/views/entity/informationParity.svelte.test.ts` is the regression gate. It
asserts SEMANTIC AVAILABILITY, never layout: given a detail payload that carries a
fact, the fact must be reachable in the rendered page. A redesign may move
anything; it may not drop a fact the backend sends. The test fails if a revision
page ever again reduces the declared surface to "3 Interfaces".

### Phase 4 reopen: visual-renderer truth and contract closure (this session)

- Starting HEAD: `8a2f7910` (the reviewed HEAD of PR #291).
- Synchronized base: `eb1482ff` (current `main` tip). `main` had NOT moved from that
  base (it equals the merge-base and is an ancestor of HEAD), so no re-sync was
  needed. Integration remains merge (branch content preserved).
- The PR stays draft. No Git history was rewritten, rebased or force-pushed.

### Phase 4 dual-UI closure + completion (this session)

- Starting HEAD: `973daa14` (the reviewed HEAD of PR #291).
- Synchronized base: `eb1482ff` (current `main` tip). `main` had NOT moved from that base
  (it equals the merge-base and is an ancestor of HEAD), so no re-sync was needed.
  Integration remains merge (branch content preserved). No Git history was rewritten; the
  U+00A7 commit-history CI enforcement stays BLOCKED (section 8 item 9). The PR stays
  draft.

An independent review of `973daa14` found six concrete gaps; each is now closed with
tests that fail before the fix and pass after, holding 100% Go coverage on the touched
packages and an error-clean svelte-check + full Vitest suite:

1. Dual-UI product surface. The SPA still served BOTH the legacy views (ServiceListView,
   ServiceDetailView, GraphPageView, OwnersView, OwnerDetailView) and the product IA on
   the same Fleet-capable host, so a legacy deep link reached a second, competing UI.
   Now, on a Fleet-capable host (`capabilities.fleet === true`), every legacy route that
   has a product equivalent canonicalizes to the product IA and the legacy view never
   mounts. The static 1:1 roots redirect through `legacyRedirectTarget` + a `replaceHash`
   (a history REPLACE, so Back never bounces and a reload stays on the product URL); the
   catch-all legacy list (and any unknown hash) redirects to the operational overview.
   Name-bearing legacy detail URLs are migrated by `LegacyEntityRedirect`, which resolves
   the display NAME through the Product Entities API and NEVER fabricates a canonical key:
   one exact match canonicalizes, several domain-qualified same-named entities show an
   explicit disambiguation, none shows an honest not-found, and a transport failure shows
   a Product error (never a fall back to the legacy screen). The command palette offers
   the product destinations on a Fleet host (and drops the legacy service/owner search,
   which the visible EntitySearch covers). The retained legacy views are the NON-Fleet
   compatibility surface ONLY (the offline `pacto doc` single-service export, now
   declared `fleet:false` so it resolves its host class definitively and never shows a
   dead Fleet tab); they are unreachable on a Fleet host. Global `api.services()` no
   longer fires for a product route -- only for the retained Compare/Readiness
   capabilities, and for the legacy views on a non-Fleet host. A dual-UI architecture
   guard (`dualui.test.ts`) proves one Services/Owners/Graph/entity destination on a
   Fleet host and legacy retention on a non-Fleet host; the WASM demo Playwright suite
   proves the canonicalization, reload persistence and Back non-bounce end to end.
2. Knowledge-view edge-payload leak. The views selector drove traversal and edge
   membership but the edge PAYLOAD still merged all knowledge, so an expected-only query
   returned observed evidence + a difference verdict, an observed-only query returned
   declared claims, and a fine-grained edge exposed service corroboration under
   expected-only. A single finalization step (`projectEdgeForViews`, applied once in the
   `Neighborhood` dispatcher) now clears the knowledge the requested views exclude:
   expected-only keeps the declared claim and nothing observed and no comparison;
   observed-only keeps the observed fact and no declared claim; expected+observed keeps
   both facts but no Difference; only a differences view carries the Difference and the
   fine-grained `serviceCorroboration`/`observationScope`. The `runs` edge (the target's
   identity link) is shown intact regardless of views. Counterexample tests per
   perspective.
3. Target dependents / effective depth. The target projection emitted inbound logical
   consumers as dependency edges, hanging a depth-2 logical component off the one-hop
   target focus and contradicting `effectiveDepth=1`. Inbound dependency knowledge is
   only available at logical-service scope (Pacto does not observe which logical
   consumers routed to a specific deployment), so the target projection now draws NO
   inbound dependents and surfaces a `DEPENDENTS_LOGICAL_SERVICE_SCOPED` limitation
   pointing to the service perspective. A `node.Depth <= effectiveDepth` invariant test
   covers every projection; the dead `addTargetLogicalDependents`/`serviceReverseDeps`
   were removed.
4. Silent focus reinterpretation. Changing the graph perspective kept the URL kind/key
   while the backend resolved (e.g.) a target to its linked revision, so the URL and the
   drawn graph disagreed and `requestedFocus` was silently replaced. `requestedFocus` now
   stays exactly what the request asked for; a new explicit `projectionFocus` ProductRef
   carries the resolved entity when it differs. The UI canonicalizes the URL on a
   perspective change (target->service via `focusService`, target->revision via the runs
   edge, revision->service), read from backend product data and never inferred from
   labels; a bookmarked reinterpreted URL canonicalizes on load via `projectionFocus`
   (a replace, so the active perspective never contradicts the graph). OpenAPI + the TS
   SDK were regenerated for the new field.
5. Stale Cytoscape presentation. `NeighborhoodGraph` keyed its refresh on topology alone,
   so a refresh that changed a node status or an edge difference (without changing the
   graph shape) left the canvas stale while the text alternative updated. It now keys on
   two signatures: a topology change rebuilds; a presentation-only change (node
   status/label/kind, edge reconciliation state) restyles the canvas in place via a new
   `patchData` control (no relayout); an identical answer recreates nothing.
6. Legend vs canvas. The legend advertised reconciliation states the canvas did not draw
   (the adapter forwarded a single dead drift bit). The adapter now carries one
   backend-derived edge visual state (from difference or service corroboration, never
   re-inferred), and the Cytoscape stylesheet renders matched / expected-not-observed /
   drift / insufficient as real line distinctions (tone + width + style) plus a dashed
   revision-node border. The legend is rewritten so every item maps to a real canvas
   distinction; the drawer and text list keep the full precise wording.

Canonical post-migration route map as it stood AT THE END OF PHASE 4. It was
superseded by the product-coherence correction (Change analysis absorbed Impact
and Compare; Readiness became a dimension). Section 3 is the authoritative route
map; this table is retained only as the Phase-4 record.

| Concept | Canonical product route | Legacy route (redirected on a Fleet host) |
|---------|-------------------------|-------------------------------------------|
| Home / Operational Overview | `#/fleet` | `#/` (and any unknown hash) |
| Services (list) | `#/fleet/services` | `#/services` |
| Service detail | `#/fleet/services/:serviceKey` | `#/services/:name` (resolved via Product API) |
| Revision detail | `#/fleet/revisions/:revisionKey` | (none) |
| Target detail | `#/fleet/targets/:targetKey` | (none) |
| Owners (list) | `#/fleet/owners` | `#/owners` |
| Owner detail | `#/fleet/owners/:ownerKey` | `#/owners/:id` (resolved via Product API) |
| Sources | `#/fleet/sources` | (none) |
| Attention | `#/fleet/attention` | (none) |
| Operational Graph | `#/fleet/graph` | `#/graph` |
| Product Impact (superseded) | `#/fleet/impact/:serviceKey` | `#/impact` (advanced raw-ref form, retained) |

Phase-4-era position on the specialized capabilities, ALSO SUPERSEDED: Readiness
(`#/readiness`) and Compare (`#/diff`) were kept on every host and participated
in the primary nav, consuming the legacy services plane as a documented retained
boundary. The product-coherence correction removed both from the Fleet primary
nav — Compare became the Change analysis workspace on canonical RevisionKeys,
and Readiness became an attention/revision dimension. Both legacy routes are now
compatibility redirects on a Fleet host and remain the real UI on a non-Fleet
one. No legacy view is a hidden second UI on a Fleet host: each renders only when
`capabilities.fleet === false`.

Removed / rewired: no legacy view is deleted (the non-Fleet `pacto doc` export is their
only host and still needs them); instead they are gated behind the non-Fleet host class
and made unreachable on a Fleet host. Dead code removed: `addTargetLogicalDependents` and
`serviceReverseDeps` (`pkg/fleet/projection.go`) and the unused `edgeColor`
(`lib/graph.ts`).

Product capability lost by the dual-UI cleanup (Part 1.4 option C), and since RESTORED:
rich-doc / Mermaid RENDERING lived only in the legacy ServiceDetailView (it fetched full
doc content over the legacy `/api/services/:name` plane and rendered it with mermaid). The
product entity pages expose bounded doc PREVIEWS (title/path), and the product API did not
carry doc content at all. So making the legacy service detail unreachable on a Fleet host
removed rendered-doc viewing from the Fleet product.

This paragraph used to close by deferring the migration to a "post-freeze product
follow-up" and marking the `mermaid.spec.ts` section-K acceptance `test.fixme` with that
reason. That was the wrong call and it is withdrawn: a capability the previous dashboard
shipped, removed by our own cleanup, is a regression to fix rather than a follow-up to
schedule -- and a `fixme` standing over it made the browser suite report green on a
capability that no longer worked. The migration is done (`Query.RevisionDocument` +
`GET /api/fleet/revisions/document` + the `RevisionDocs` product viewer; see the
`DocsSection` doc-BODIES row in section 0c and "Second correction pass" below), the
`fixme` is gone, and `mermaid.spec.ts` now proves rendering through the PRODUCT path on a
Fleet host. The capability also still works on the non-Fleet `pacto doc` export, which is
unchanged.

### Phase 5 progress (this session)

Phase 5 (responsive + accessible interaction) was carried substantially forward this
session, directly after the Phase-4 completion. Delivered and verified (svelte-check
clean, full Vitest green, and the built-WASM-demo Playwright suite green on both the
desktop and mobile projects):

- Semantic accessibility: `FleetEntityView` gains a page-level h1 (via a reusable
  `.visually-hidden` utility), so every product page has one useful h1. Global shortcuts
  no longer steal interaction (requirement 8.5): `isTypingTarget` now includes SELECT
  (native type-ahead), and Cmd/Ctrl-K no longer stacks the palette over an open Search
  overlay.
- Graph accessibility model (8.2): the Cytoscape canvas is described as an image
  (role="img" with a meaningful label) instead of declaring an incomplete
  role="application"; the accessible keyboard/screen-reader model is the first-class
  Relationships navigator (the text alternative), whose node/edge controls open the same
  quick-inspection drawer.
- Drawer focus model (8.3): the graph quick-inspection drawer is a non-modal panel
  (no focus trap); Escape and Close close it and return focus to the control that opened
  it, and opening it moves focus into the drawer.
- Modal dialogs: EntitySearch gains a real Tab focus trap and both EntitySearch and the
  command palette are focusable dialogs (tabindex).
- Reduced motion (8.6): the graph layout, fit, center and expand/collapse animations
  honor prefers-reduced-motion (they become instant), joining the already-safe logo spin,
  D3 charts and global CSS transitions.
- Color and semantic state (8.8): inline prose links are underlined (WCAG 1.4.1 /
  link-in-text-block), so a link is distinguishable from surrounding text without color.
- Automated axe gate (8.9): `@axe-core/playwright` (pinned) runs WCAG 2.0/2.1 A+AA checks
  over Overview, Services, Service detail, Attention, Product Impact, graph discovery, the
  focused visual graph, the graph drawer open and the mobile navigation open. It is wired
  into the existing blocking dashboard browser CI (the desktop + mobile Playwright
  projects). The only disabled rule is color-contrast, narrowly and by design: requirement
  8.8 says not to claim formal contrast conformance without measurement, so contrast is
  handled by design tokens rather than asserted by the automated gate.
- Keyboard acceptance (8.10): a keyboard-only spec proves "/" and Cmd/Ctrl-K behavior
  (including not hijacking a typed input), full graph node/edge selection + inspection +
  Escape-restores-focus, keyboard-operable knowledge/direction controls, and that the
  one-hop target projection exposes no depth/expand control even for a hand-crafted
  depth=6 URL.
- Responsive acceptance (8.11): a spec asserts no body-level horizontal scrolling at 320
  and 375 px across Overview, Services, Attention, Owners, Sources, Impact, service
  detail (the shared entity-page layout), graph discovery and the focused graph, plus a
  wrapping graph toolbar.
- Deterministic unit coverage for the above (shortcut guards, drawer Escape/focus return,
  the EntitySearch trap, the graph signatures and the img-role) is in the Vitest suite.

### Phase 5 closure: the per-route heading / landmark pass (8.1)

The last open Phase-5 item was the exhaustive per-route document-structure audit. It
is done, and it found three defects the existing gates structurally could not see:
`axe.spec.ts` samples STATES (correct for contrast and widget semantics, which vary by
state), while document structure varies by ROUTE.

The audit walked all eighteen canonical product routes plus the retained non-Fleet
compatibility surface in the built WASM demo. What it found:

1. `document.title` was the literal string "Pacto Dashboard" on every route. WCAG 2.4.2
   asks a page to be identifiable by its title, and a screen reader announces the title
   on navigation; tabs and history entries were also unusable. Fixed in
   `lib/pageTitle.ts` by MIRRORING the rendered h1 rather than maintaining a parallel
   route-to-title table -- a second copy of the page name would be free to drift from
   the heading the user can see, and mirroring covers the legacy views for free. A
   MutationObserver drives it because a detail heading only exists once its request
   lands.
2. Change analysis skipped h1 -> h3. The cause was not that view: `EmptyState` hard
   coded `<h3>`, so EVERY screen whose main content is an empty state skipped a level.
   Fixed once in the shared component and its product wrapper (a `level` prop, default
   2), not per caller.
3. Every route exposed TWO navigation landmarks -- one named "Primary", one unnamed
   wrapping it -- and NO banner. So "skip to main content" had nothing to skip past,
   and landmark navigation offered two indistinguishable "navigation" entries. The app
   chrome is a banner and now says so (`<header class="navbar">`).

Also fixed: the entity page had no h1 at all while loading, while erroring, or when the
entity does not exist -- exactly when a user most needs the page to name itself. The
heading moved out of the ready branch; kind and requested key are known before the
request resolves.

The durable gate is `e2e/headings.spec.ts`. Per route it asserts one non-empty h1 in
`main`, no skipped heading level, a `document.title` that contains the heading and is
not the generic fallback, exactly one main landmark, exactly one banner, and uniquely
named navigation landmarks -- then runs the axe structural rule subset
(`page-has-heading-one`, `heading-order`, `empty-heading`, `document-title`, the
`landmark-*` rules, `bypass`). Two design points make it trustworthy: landmarks go
through Playwright's ROLE engine rather than a tag selector (a `<header>` is a banner
only outside sectioning content), and each route is entered by a REAL navigation --
`page.goto` to a URL differing only in its fragment is a same-document navigation, so
a fragment-only sweep would audit the first page eighteen times and pass. The spec
proves it actually moved by requiring the collected titles to be mostly distinct.

**Phase 5 is COMPLETE.** All of its acceptance is now green: measured AA contrast
enforced by axe in both themes, the keyboard graph model, the drawer focus model,
reduced motion, the 320/375px responsive sweep, the automated axe gate, and this
per-route structural sweep. Two things are recorded as deliberate SCOPE, not debt:
the accessible graph navigator is the semantic Relationships list (8.2's bar is "every
node and edge operable and inspectable by keyboard", which is met -- arrow-key spatial
traversal of the canvas is not built), and testing is on emulated widths only, no
physical devices, as the task requires.

### Product-coherence correction, second pass: visual coherence

(Historically headed "Phase 6 second pass". Renamed to keep one canonical program
sequence — see section 0a. Whole-program Phase 6 is WASM browser acceptance.)

The first coherence pass fixed the CONCEPTUAL incoherence (vocabulary, IA, dead legacy
screens). Re-capturing the 30 screens afterwards and reading them side by side exposed the
remaining PRESENTATIONAL incoherence -- the same control drawn several ways, which is what
"several generations of UI stitched together" looks like at the pixel level. Each item
below is one shared idiom replacing N private copies, and each was found in a screenshot,
not in a file:

- **One disclosure.** "One disclosure away" is load-bearing in five places (canonical
  identifier, advanced filters, revision-match breakdown, confidence legend, graph text
  alternative). Each had grown its own styling: an accent-coloured link, quiet grey, an
  inherited default, a hand-rolled caret -- and the one whose summary was `display: flex`
  had silently lost its native marker, so it read as a dead label rather than something
  openable. Now one `.disclosure` class in `styles/components.css`, one caret, one
  rotate-on-open, one reduced-motion opt-out. Guarded by a coherence test in
  `architecture.test.ts` that fails if a product `<details>` skips the shared class.
- **One button.** `GraphView` had re-implemented `.btn` as `.gv-btn`, and the copy was
  broken: `.gv-btn` was declared AFTER `.gv-btn-primary` at equal specificity, so the
  graph's primary Search button rendered as a plain surface button while the identical
  control one nav tab away rendered accent. Three list views carried byte-identical
  `.lv-btn` / `.sv-btn` clones. All four now use the shared `.btn` / `.btn-primary`.
- **One status vocabulary.** The services and attention filters each hand-listed four of
  the seven wire statuses, and both omitted `NotEvaluated` -- the value most rows in the
  services list actually carry, so the list could show a state the filter could not select.
  `STATUS_FILTER_OPTIONS` in `format.ts` is now derived from `STATUS_LABELS`, worst-first,
  excluding only `Reference` (a bundle role, not an assessment outcome).
- **One tile source.** The Overview's observed-only tile was hand-written beside its own
  backend entry point, so the same count reached the screen twice under two labels, two
  cases and a locally guessed tone. Every tile is now rendered from an `EntryPoint` with
  the backend's own label, count, severity and href -- filtered to the ones that lead
  somewhere the user is not (the uncategorised attention entry point is the lead tile's own
  number, and the overview entry point links back to the page it sits on, where the source
  health strip already shows what it counts).
- **One micro-label shade.** `.attn-next-k` carried `opacity: 0.75` on `--c-text-3`,
  landing at 3.58:1 -- below WCAG AA, and a real axe failure in the light-theme audit. It
  was also the only uppercase micro-label in the product with its own shade. Removed.

The nav label is **"Operational graph"**, sentence case, matching every other nav item and
breadcrumb. Section 0b's decision record types it Title-case ("Operational Graph"); the
rendered string is sentence case deliberately, and the two e2e specs that asserted the
exact nav text were updated with it.

Not changed, deliberately: `EmptyState` and `.disco-placeholder` are two shared components
for two genuinely different states (nothing exists / nothing selected yet), not a
duplication; the ServiceEntity "Expected dependencies" cards above "Observed traffic and
differences" are summary-then-detail; `src/sections/**` keeps its own presentation because
that is the non-Fleet `pacto doc` host. Only `.disclosure` has an automated coherence
guard -- the shared `.btn` does not, so a fifth private button clone would not fail CI.

### Visual-intelligence audit (requirement 6 and 7)

The redesign over-corrected: the product surfaces became clean, correct lists and lost
the at-a-glance comprehension the old dashboard had. This audit classifies every old
visualization and every current product surface as RESTORE / EVOLVE / RETIRE. The rule
applied throughout: **a proportion may only be drawn from a backend aggregate over a
COMPLETE population.** Bounded previews (`MaxDetailPreview` = 200, list pages = 25) are
lists, not denominators, and a chart drawn from one would report "the first 200 targets"
as the estate.

Old dashboard visualizations (`src/lib/charts.ts` and its Svelte wrappers, still live on
the non-Fleet legacy host, none reachable from the Product IA):

| Old visualization | Verdict | Where it went |
|---|---|---|
| `renderCategoryStackedBars` / `CategoryBreakdownChart` | EVOLVE | The numbers survive as graded `EntryPoint` tiles on the Overview and as the complete "By category" `HorizontalBars` on the attention workspace. |
| Summary bar (status split) | RESTORE | `DistributionBar`, now the shared four-bar `PostureBars` block. |
| `renderOwnerBars` / `OwnersBarChart` | EVOLVE | Became per-owner posture on the owner detail page, backed by the new `OwnerSummary` aggregate. A fleet-wide owner bar chart is not restored: the owners LIST is capped at 25 and `EntityRef` carries no counts, so any chart drawn there would be page-derived. |
| `renderReadinessDonut` / `ReadinessDonut` | RETIRE | Readiness is per revision, not a fleet-scoped population; a donut over mixed revisions counts a thing that does not exist. Also a pie by another name. |
| `renderHeatmap` / `ReadinessHeatmap` | RETIRE | Same reason, plus colour-only meaning at cell size. |
| `renderTreemap` / `TreemapChart` | RETIRE | Superseded by Change analysis impact, which answers the same "what does this touch" question against canonical `RevisionKey`s instead of area-encoded name boxes. |
| `renderPriorityQuadrant` / `PriorityQuadrant` | RETIRE | Both axes were an invented composite score. The product grades attention items with the backend's own severity. |
| `renderVersionTimeline` / `VersionTimeline` | RETIRE as a timeline, EVOLVE as a list | The snapshot has no publication history (see "Deliberate non-parity"); the chronology is served by the bounded revision-history API and rendered as an ordered list. |

Current Product surfaces:

| Surface | Verdict | Outcome |
|---|---|---|
| Overview | EVOLVE | Asked two of the three orthogonal questions. `OverviewSummary.Evidence` adds the freshness partition (fresh / stale / never observed), so "how recently did we look" is a proportion and not just a stale count. `Evidence.Stale` equals the pre-existing `StaleTargets` by construction, pinned by a test. |
| Service detail | RESTORE | Kept its four distributions, now drawn by the shared component. |
| Owner detail | RESTORE | Was four bounded lists and no posture. New `fleet.OwnerSummary` (complete service / revision / target populations, accumulated in the same walk that builds the previews) drives the same four bars. Gated by `informationParity.svelte.test.ts`. |
| Target detail | RETIRE | A single entity is not a population, and its findings preview is truncated. Facts, not charts. |
| Source detail | RETIRE | Two counts and a status is not a chart. |
| List workspaces | unchanged | Aggregate context allowed, page-scoped charts must be labeled page-scoped (requirement 8, already closed). |

Shared implementation: `components/viz/PostureBars.svelte` renders the three orthogonal
questions plus findings-by-severity in one visual language, so fleet, service and owner
cannot drift in wording, ordering or colour -- and none of them ever collapses into a
single "health score". Every non-clean bucket is a keyboard-operable drill-down into the
attention backlog SCOPED to the surface that drew it: the attention endpoint already
accepted `service` and `owner`, so this was a routing change (`fleetAttentionUrl({
service })`, parsed back into a filter chip) rather than a new API.

No third rendering of the attention categories was added. `AttentionPreview` carries no
population-complete category tally, and the two places that do have one already draw it;
a third would be a dead infographic by duplication.

### WASM demo richness (requirement 16)

A demo whose every target is compliant, unlabelled and observed by one collector shows a
product with empty distributions, empty runtime inspectors and an empty source list -- and
that is what it showed. `examples/demo/source_fleet.go` now builds the snapshot from five
sources instead of two, through the same `fleet.Build` pipeline the CLI and the operator
use: a bundle registry (declared), a cluster collector (running), a telemetry collector
that corroborates two of those targets and contributes the observed call edges, a partial
registry mirror and an unreachable edge cluster. The nine targets between them cover every
compliance verdict, exact / inferred / unresolved revision matching, fresh / stale /
never-observed evidence, one service running in two scopes, three finding severities, and
the labels and observed runtime values the target page exists to show.

Two engine behaviours shaped the fixture, and both are recorded because they are easy to
re-break:

- The **fresher** contribution owns the evaluation fields. Telemetry does not evaluate
  compliance, so the telemetry observations are deliberately older than the cluster's; a
  fresher telemetry contribution replaces real verdicts with empty ones and the whole demo
  goes Unknown.
- An **exact revision match** and **retrievable content** are different facts. The one
  exact target carries both (a real embedded digest and a canonical digest ref); the
  tag-referenced targets are confident matches over mutable content; the ref-less ones are
  matches with nothing to fetch.

Deliberately NOT added, each because the fixture would have to lie to produce it: an
ambiguous link (needs two revisions sharing a version, which the demo's bundle set does
not contain), a quarantined target (needs two sources contradicting each other -- shipping
a self-contradicting fleet as the reference example teaches the wrong lesson), an invalid
revision (every demo bundle is validated by `make -C examples/demo validate`), and
multiple domains (the demo has one registry, so one domain is the truth).

`examples/demo/smoke.mjs` asserts the richness through the PRODUCT endpoints the UI
actually calls, not the raw snapshot: a demo that is rich in `/api/fleet/snapshot` and thin
in `/api/fleet/overview` still renders an empty product. It runs in CI (`docs.yml`).

### Phase 6 coverage reconciliation (requirement 20)

Browser coverage had grown opportunistically: each correction pass added the spec that
proved its own fix. That is how it should grow, but it means the total is not automatically
the same thing as the Phase-6 criterion ("WASM browser acceptance"). Below, the existing
specs are mapped onto what Phase 6 actually has to demonstrate, and the gaps that mapping
exposed are named with the spec that now closes each one.

| Phase-6 criterion | Covered by | Status |
|---|---|---|
| The real Svelte bundle boots against the real engine in a real browser | every spec (`playwright.config.ts` serves the built `examples/demo/dist`) | pre-existing |
| The product IA is navigable end to end by a first-time user | `novice-journeys.spec.ts` (J1-J12) | pre-existing |
| Legacy screens never mount on a Fleet host | `novice-journeys.spec.ts` (`LEGACY_MARKERS`, J12) | pre-existing |
| The Operational Graph actually paints | `graph-visual.spec.ts` (non-headless canvas, pixels, counts) | pre-existing |
| Graph spatial state survives refresh / reload / semantic update | `graph-state.spec.ts` (13.4 A-L) | pre-existing |
| WCAG A/AA, both themes, on real product states | `axe.spec.ts` | pre-existing |
| Keyboard operability of graph and global affordances | `keyboard.spec.ts` | pre-existing |
| 320 / 375px, including populated interactive states | `responsive.spec.ts`, `mobile.spec.ts` | pre-existing |
| Heading / landmark structure per canonical route | `headings.spec.ts` | pre-existing |
| **Bounded rendering under a population far larger than a page** | `product-scale.spec.ts` | ADDED |
| **Honest truncation and paging messaging at that scale** | `product-scale.spec.ts` | ADDED |
| **Hostile identities render as text and keep their canonical key** | `product-scale.spec.ts` | ADDED |
| **The visualization contract holds on composed pages, both themes** | `viz-acceptance.spec.ts` | ADDED |
| **Reduced motion actually reaches the rendered bars** | `viz-acceptance.spec.ts` | ADDED |
| **Recorded render baselines** | `product-scale.spec.ts` | ADDED |
| **A background refresh keeps the user's place (poll, explicit Refresh, same-route data change)** | `place.spec.ts` | ADDED (second pass) |
| **A failed background refresh keeps the stale page and says so** | `place.spec.ts` | ADDED (second pass) |
| **Hard reload and Back/Forward restore position on canonical deep links** | `place.spec.ts` | ADDED (second pass) |
| **Doc BODIES render on a Fleet host, Markdown and Mermaid, through the product path** | `mermaid.spec.ts` (migrated off `test.fixme`) | REPAIRED (second pass) |
| **Contract references are navigable, including the same-name/cross-domain case** | `references.spec.ts` | ADDED (second pass) |
| **Search-as-you-type: suggestions, keyboard, same-name domains, no-result, stale ordering, mobile** | `suggest.spec.ts` | ADDED (second pass) |

The three "second pass" groups exist because Phase 6 had been closed with a
`test.fixme` standing over a capability the previous dashboard shipped, and with no
acceptance at all for refresh behaviour, reference navigation or the search field.
A criterion nobody wrote a spec for is not a criterion that passed. See "Second
correction pass" below.

Two notes on what these specs deliberately do NOT do.

They do not assert a millisecond budget. No requirement derives one, the runner's speed is
not controlled, and a threshold nobody derived becomes a flake that gets raised until it
means nothing. The numbers are printed and recorded below; the assertions are on invariants
that hold at any speed -- the rendered row count stays at the page size however large the
answer, and the document does not grow as the user pages.

They do not mock the product. The scale and hostile-identity cases amend one response, in
the page, through the `window.__pactoServe` seam `graph-state.spec.ts` established (a
wasm-served fetch is invisible to Playwright's network interception). Everything else --
parsing, routing, rendering, paging -- is the real app on the real bundle.

The visualization audit is written to cover figures it has never seen: it walks every
`figure.dist` / `figure.hbars` on each surface and applies the whole rule (a caption
heading for the accessible name, every graphic `aria-hidden`, every row's label and exact
value as text, no page with neither rows nor an explicit empty state). A chart added later
is covered the day it ships. The closed-set guard on `components/viz/` and the retired-chart
guard on product surfaces live in `src/lib/architecture.test.ts`, because "no donut came
back" is a source fact, not something a browser can prove by absence.

#### Recorded render baselines

Measured on the built WASM demo in Chromium, desktop project, single worker, on the
reviewed build. These are a baseline to compare against, not a gate.

| Surface | Wall clock | DOM nodes |
|---|---|---|
| Cold boot (wasm instantiate + Overview render) | 460 ms | 312 |
| Overview (warm route render) | 9 ms | 313 |
| Services list | 15 ms | 220 |
| Attention | 17 ms | 458 |
| Service detail | 21 ms | 341 |
| Operational Graph, focused (Cytoscape paint) | 40 ms | 248 |
| Change analysis | 7 ms | 96 |
| Services list, 25,000-service population | 409 ms | 333 |
| Attention, 25,000-item backlog | 401 ms | 509 |

The line that matters is the last two against their demo-data equivalents: a thousandfold
larger population costs a bounded number of DOM nodes (220 to 333, 458 to 509 -- the
difference is wider numbers and a longer range line, not more rows). The wall clock rises
because those runs include a fresh page load and the interceptor's work, not because more
was rendered.

### Product UI design freeze (requirement 19)

The broad Product UI migration is CLOSED as of this session. The IA, the vocabulary, the
entity model, the four primary workflows, the visualization system and the graph
interaction model are FROZEN.

Frozen means: changes from here are concrete bugs and concrete counterexamples -- a
behaviour that contradicts a stated invariant, an information-parity regression against the
matrix in section 0c, a failing acceptance, an accessibility defect. Not another taste
pass, and not a re-litigation of a decision already recorded here. The accepted decisions
are enumerated in section 0a (product-coherence correction) and section 0b (vocabulary);
reopening one needs a new concrete counterexample, not a new preference.

What the freeze does not cover, because it was never Product UI design: the remaining
whole-program phases in section 5 (7 onward), and the ordinary maintenance of the surfaces
already shipped.

What the freeze also does not cover, learned the hard way in the second correction pass
below: a capability the PREVIOUS dashboard shipped and this migration dropped. That is a
regression, and the freeze exists to stop taste passes, not to make a regression
permanent. Two of the five defects below are exactly that shape (rendered doc bodies,
navigable contract references), and both had been recorded in this file as deliberate
scope rather than as loss. Restoring a lost capability is a bug fix under the freeze. So
is fixing a field that behaves worse than the one it replaced -- which is why the
Services search field gained suggestions rather than a redesign.

### Post-freeze correction pass: six defects the terminal could not have found

The freeze allows exactly one kind of change: a concrete defect. The final real-user
inspection produced six, and every one of them was visible in a screenshot while the whole
test suite was green. They are recorded together because they share a shape -- a number or
a word on a page disagreeing with the rows directly beneath it, which no unit test asserts
because no unit test looks at two components at once.

| Defect | Where it was seen | Root cause | Fixed in |
|---|---|---|---|
| The Services compliance bar was one flat grey block above rows badged Compliant, Unknown and Not compliant | Services list | the page bucketed statuses with its own lowercase spellings; the wire is PascalCase, so all 16 rows fell through to "Other" | `lib/distributions.ts` (shared `tallyStatuses` plus a Not-evaluated bucket), consumed by `FleetServicesView` |
| "13 declared dependencies" above an Expected dependencies card reading "3 of 3" | Service page | a declared relationship is per-revision, so the tally counted declaration records, not dependencies -- the number grew with release history | `pkg/fleet/aggregate.go` |
| "1 items", "1 consumers" | Attention, Change analysis, Revision | ranked charts are mostly small numbers, so the singular is the common case, and an irregular noun cannot be recovered by stripping an "s" | `components/viz/HorizontalBars.svelte` (`unitOne`) |
| "Fleet posture" above a page about services; "how this fleet knows" above a list of consumers | Service, Overview, Change analysis, entity lists, Graph | the internal name leaking into rendered copy, which section 0b forbids | the five surfaces, locked by a product-vocabulary guard in `architecture.test.ts` |
| "USED BY api-gateway" twice, indistinguishable, and 8 relationship rows for a service with 3 dependencies and 2 dependents | Service page, Operational target page | a neighborhood is a graph: at depth 1 it carries edges BETWEEN two neighbours, which are somebody else's relationships, and a flat list labelled by counterpart reads them as this entity's | `pkg/fleet/preview.go` |
| The same duplicate rows on the target page | Operational target page | same root cause; fixed once in the shared preview rather than per view | `pkg/fleet/preview.go` |

Two of the six are backend semantics, so they are fixed in `pkg/fleet` and the API, the
MCP tools and the dashboard all get the correction from one place. Each has a test that
fails before the fix: the relationship one was proven non-vacuous by neutering the guard,
which reproduces the neighbour-to-neighbour edge in the list.

The inspection's own questions, answered against the AFTER captures: the system is
legible without reading every table (the Overview leads with attention counts and three
labelled distributions); the full contract is inspectable at precision on the Revision
page (interfaces with operations, configuration values, policies, capabilities, workload
facts, validation findings, declared dependencies with ref/required/compatibility/pinned,
tools, docs, metadata); information now sits on the entity it describes (this pass moved
two counts back); the graph preserves the mental map across a semantic refresh and a full
browser reload (identical node positions in captures 09/10/11); nothing that V1 showed is
missing without a recorded reason (section 0c); no surface still looks like the old UI;
and no chart is decorative -- each is a population partition or a ranked count with its
exact numbers as text.

### Second correction pass: five regressions a green suite reported as shipped

Starting HEAD: `2efeb9ef` (the independently reviewed HEAD of PR #291). Exact-SHA CI
was green there and most of the redesign was real, and a second real-user review still
found five concrete regressions. Two of them are capabilities the PREVIOUS dashboard
had, and this file had recorded both as deliberate scope. That is the lesson of this
pass, and it is why the entries below name what the ledger said as well as what the
code did: **a regression written down as a boundary stops being reviewable.**

What was reopened, narrowly: rich entity / information parity for docs and references,
interaction for refresh / scroll / autocomplete, and the WASM browser acceptance for
those. The proven engine, identity, fleet-query, graph-projection, boundedness and
evidence semantics were NOT reopened and are untouched.

| Regression | Root cause | Fixed in |
|---|---|---|
| A background refresh threw away the page the user was reading: the body vanished, the page collapsed and the scroll offset was clamped toward the top, several times a minute, untouched | `FleetEntityView.load()` set `loading = true; error = null; detail = null` on EVERY tick, and `App.loadGlobal()` advances that tick on the poll timer. The lifecycle had one event where it needed four; and `decideViewState` ranked "a request is in flight" above "we already have the answer", so the collapse was decided in the shared state machine, not in one view | `lib/knowledgeState.ts` (stale-while-revalidate decided once, for every view, with `revalidating` / `refreshError` reported ALONGSIDE the data), `views/FleetEntityView.svelte` (identity vs re-ask, via the shared `createProductLoader` and a `dataTag` so another entity's answer never renders under this heading), `lib/scrollRestore.ts` (route-scoped restoration for the cases the browser genuinely cannot do: async content on a hash router) |
| Rendered doc bodies (Markdown + Mermaid) were gone on a Fleet host | the capability lived only in the legacy ServiceDetailView over `/api/services/:name`, which the dual-UI cleanup correctly made unreachable — and the replacement was recorded as a post-freeze follow-up instead of being built, with a `test.fixme` standing over the acceptance | `pkg/fleet/document.go` (`Query.RevisionDocument`: keyed by canonical RevisionKey, path must be one the revision itself published, `MaxDocumentBytes` bound, explicit `DocumentUnavailableError`), `pkg/dashboard/fleet_product.go` (`GET /api/fleet/revisions/document`, 404 unknown / 422 unavailable), `components/RevisionDocs.svelte` (lazy per-document read, shared sanitized `MarkdownView` + Mermaid) |
| Contract references were inert text: a configuration or policy pointing at another service could not be clicked | two causes, one visible. The row rendered the raw ref with no destination — but underneath, `resolveRefService` matched on the DECLARED NAME only, so a scope called "platform" pointing at `platform-app-config` never resolved and the backend had no destination to give. Almost every realistic reference was unresolved and nobody noticed, because nothing rendered the resolution | `pkg/fleet/build.go` (`refServiceName` reads the destination out of the REF, still resolved strictly inside the referring revision's own domain, so two same-named services in two domains resolve to their own), `pkg/fleet/contractdetail.go` (`RefResolution` published on config scopes and policies), `pkg/fleet/detail.go` (`ReferencedBy`: the reverse direction, which the old page never had), `components/ContractReference.svelte` (raw ref always visible, canonical `EntityLink` when resolved, honest unresolved state otherwise, nothing inferred from a label) |
| The Services search field was submit-only: type a name, press Search, hope | it was the one search surface that never got the product Entities query; the global palette had it | `lib/entitySuggest.svelte.ts` (the debounce, the bound and the stale-response guard extracted from `EntitySearch` and now shared by both surfaces, so the race is fixed once), `components/EntityCombobox.svelte` (ARIA 1.2 combobox; suggestions only — the filter or the navigation commits on an explicit choice, so a keystroke writes no history entry), `views/FleetServicesView.svelte` |
| Four unresolved CodeQL threads on `lib/architecture.test.ts` ("incomplete multi-character sanitization", "bad HTML filtering regexp") | the product-vocabulary guard extracted the reader-visible words from a component by DELETING the script, the stylesheet, the comments and the class attributes with a chain of regex replacements. It was never a sanitizer — it is source analysis, in a test, over files on disk — but it was shaped exactly like one, and a shape is what a static analyzer can see | `lib/architecture.test.ts`: `readerVisibleText` now walks the Svelte compiler's own modern AST (`parse(source, { modern: true })`), which hands back the script, the stylesheet and the template as separate things, so nothing is removed and the analysis says structurally what it always meant. A self-check test proves the extraction on all six placements at once (text / title / expression visible; script / comment / class not). No CodeQL rule is suppressed |

Where the counterexamples live: `views/FleetEntityView.svelte.test.ts` and
`lib/knowledgeState.test.ts` (a refresh over data on hand stays `ready`, a FAILED
refresh stays `ready` with `refreshError`, another entity's answer never renders under
this heading), `lib/scrollRestore.test.ts`, `pkg/fleet/document_test.go` (traversal,
absolute path, another revision's path, oversized, non-UTF-8, bundle not retained),
`pkg/fleet/refresolution_test.go` (same name in two domains resolves to its own),
`pkg/dashboard/fleet_document_test.go`, `lib/entitySuggest.test.ts` and the three new
browser specs `place.spec.ts`, `references.spec.ts` and `suggest.spec.ts` plus the
migrated `mermaid.spec.ts`.

The WASM demo gained a second domain (`examples/demo/partners/`) rather than a test-only
fixture, because the same-name/cross-domain case cannot be proven against a fleet that
has one domain — and a doc body served through a frontend fixture would prove the viewer
and not the product mechanism.

### Third correction pass: four invariants a green suite could not see

Starting HEAD: `13810112`. Exact-SHA CI was green there and the second correction pass
had genuinely landed. An invariant review of that HEAD found four counterexamples, and
three of them share a shape worth naming: **the product answered confidently and
wrongly instead of failing.** A test suite cannot see that shape, because every
assertion it makes is satisfied by the confident answer. Only a counterexample can.

Phases 3, 5 and 6 were reopened for exactly these and nothing else.

#### A. A document body must be immutable under a revision identity

A `RevisionDocument` read opened `rev.bundle.FS` lazily, and for a LocalSource that FS
is an `os.DirFS` over a directory a human is editing. The same
`(SnapshotID, RevisionKey, path)` therefore returned today's draft under yesterday's
revision identity — a snapshot read model quietly serving a view over mutable external
state.

Architecture chosen: **fingerprint at Build, verify at read.** Build records a SHA-256
per listed document (32 bytes each, not the prose, so a snapshot never grows by the
size of the documentation) and every read re-derives the digest of the bytes it just
read. Equal digest, serve them; anything else, `DocumentUnavailableError`. That keeps
`Query` a deterministic read model over an immutable snapshot and keeps the 512 KiB
`MaxDocumentBytes` bound and the canonical `RevisionKey` boundary exactly as they were.
Eagerly embedding bodies was rejected outright: it would put unbounded author prose in
a snapshot that is held in memory and shipped over the wire.

Behaviour, by backing store:

- Local `os.DirFS`: a post-Build edit is not served. Explicit unavailability.
- In-memory bundle FS: same — the fingerprint does not care where the bytes came from.
- OCI-loaded immutable bundles: unaffected, because their bytes never change and the
  digest always matches.
- Deleted after Build: explicitly unavailable, never silently absent.
- Unreadable at Build (no fingerprint could be taken): never served later either. A
  document with no recorded identity is not a document whose identity we can check.

`mergeRevision` was audited and fixed with it. Adopting a `Docs` list now requires
adopting the filesystem that backs it — the two travel together or not at all, so
hidden bundle selection can never decide which bytes a read returns while `SnapshotID`
stays identical. Two sources contributing the SAME immutable revision with DIFFERENT
document content serve neither and say so, in either collection order; source
permutation cannot change the answer. A runtime-only contributor (no FS, no docs) is
not a conflict, it is simply silent.

Acceptance: `pkg/fleet/document_immutable_test.go`, 11 tests, covering each bullet
above plus the two-order permutation and the differing-doc-set case.

#### B. A reference link is a claim about identity, so it needs evidence of identity

`resolveRefService` resolved a config/policy reference by taking the ref's repository
basename (or, failing that, the reference entry's own `Name`) and looking for a
same-domain service called that. Both are guesses. A repository is named by whoever
pushed it, so `oci://ghcr.io/acme/shared-config-contract` may publish a contract whose
`service.name` is `platform-settings` — and an unrelated service genuinely called
`shared-config-contract` may exist in the same domain, so the heuristic both MISSES the
real destination and INVENTS a false one. `ReferenceRef.Name` is worse: for a
configuration it is the SCOPE name and for a policy the POLICY ENTRY name, author-chosen
labels for a slot in the REFERRING contract. A policy entry called "payments" became a
link to the payments service.

The authoritative source of destination identity is now, and only, an immutable content
identifier for the referenced bundle:

1. the `digest` (or `contentHash`) `pacto.lock` recorded when it actually resolved,
   pulled and hashed that bundle — the same authority dependency resolution already
   used; or
2. a digest the author pinned in the ref itself (`oci://repo@sha256:...`), which IS the
   content address of exactly one bundle.

That identifier is matched against the revisions the snapshot already holds, inside the
referring revision's own domain. The matched revision was BUILT from its contract, so
its `service.name` is fact, not inference. Both heuristics are gone: no
repository-basename promotion, no `ReferenceRef.Name` promotion, no arbitrary
same-domain winner. An identifier matching several revisions in one domain is
AMBIGUOUS, not won. An identifier matching none is UNKNOWN. Both render as unresolved,
and the raw authored ref stays visible in every case — resolution never replaces
contract text. `ReferencedBy` (the reverse direction) inherits all of it, so a
name-collision destination never appears in another domain's list. Dependency
resolution is untouched.

This is enforced in the BACKEND relationship, not the frontend: the UI links to what
the backend resolved and has no resolution logic of its own to disagree with.

Acceptance: `pkg/fleet/refresolution_test.go`, 14 tests, including repo-name is not
service-name, an unrelated repo leaf that collides with a real service name, policy
entry name is not identity, cross-domain identity does not resolve, ambiguity is not
arbitrarily won, content hash resolves, digest-pinned ref resolves without a lock, and
the two `ReferencedBy` domain-scoping cases.

The demo fixture had to be corrected with it, and the way it failed is worth recording.
`examples/demo/genlocks` pinned each reference to a hash over the whole bundle FS,
while the demo registry addresses bundles by `sha256` over the raw `pacto.yaml` — two
different identifiers. Under the new rule every demo reference correctly reported
itself unresolved: the reader was being honest about a lock pointing at an artifact
nobody publishes. The generator now records the registry's content address (what real
`pacto lock` does with the OCI manifest digest) and indexes the two bundle trees
separately so a closure never resolves across the domain boundary the partners tree
exists to test. A reference it cannot resolve inside its own tree is left unpinned
rather than aborting the run, because the partners domain deliberately publishes no
http policy and "no pin" is exactly how an unresolvable reference is represented.

#### C. Retained rows must answer the question that is on screen

Stale-while-revalidate keeps the previous answer rendered while a new request is in
flight. That is correct for a RE-ASK of the same question and wrong for a NEW one: a
committed filter, a page change or a scope change left the previous population on
screen under the new controls, and a user reading it had no way to tell.

`decideViewState` was deliberately NOT changed to guess query identity. The caller owns
the question. Every `createProductLoader` consumer now defines two strings:
`queryIdentity` (everything that changes WHAT is being asked) and the request key
(`queryIdentity` + `refreshTick`, which only RE-asks it), calls
`loader.sync(requestKey, queryIdentity)`, and treats retained data as current only when
`loader.dataTag === queryIdentity`. Per view:

| View | `queryIdentity` |
|---|---|
| `FleetServicesView` | text, owner, status, domain, page offset |
| `FleetAttentionView` | category, severity, status, owner, source, service, stale-only, page offset |
| `FleetOwnersView` | text, page offset |
| `FleetSourcesView` | text, source health, page offset |
| `FleetEntityListView` | kind, service scope, text, status, scope, page offset |
| `FleetEntityView` | kind + canonical entity key (already correct; audited, unchanged) |

Same question: rows and scroll survive, and a poll that FAILS keeps the rows AND says
so. The honest-stale line was a one-off inside `FleetEntityView`; it is now the shared
`components/StaleRefreshNotice.svelte`, because "you are reading the last answer we
received" must say the same thing everywhere it is true.

Acceptance: `e2e/swr.spec.ts`, 8 tests. Proving these needed a way to HOLD a request in
flight, which Playwright's `page.route()` cannot do here — the demo's API calls are
served synchronously in-page by the wasm export, so no network interception ever sees
them. The spec installs a `window.fetch` gate via a getter/setter pair (returning the
native fetch until `boot.js` assigns its shim, so the shim's own fallback path never
recurses through the wrapper) and a `__pactoServe` wrapper that inflates the service
population so real pagination exists. The tests: a Services filter committed mid-flight;
Services pagination mid-flight; an Attention filter change; an Owners search change; a
scoped Revisions inventory change; a same-query automatic poll preserving rows and
place; a same-query poll FAILING and preserving rows with an honest indication; and the
pre-existing stale-response generation guard.

#### D. A place belongs to a history entry, not to a URL

Scroll positions were keyed by hash. Three entries, two showing the same page: visit A
and read to 800, push B, push A again (a fresh visit, correctly starting at 0) — the
fresh 0 overwrote the stored 800, and Back Back landed the reader at the top of a page
they had read halfway.

Positions are now keyed by `"<entryIndex>|<hash>"`. The entry index is stamped into
`history.state` on push and read back on traversal, so:

- a PUSH takes the next index, discards any forward entries' positions and starts at 0;
- Back/Forward restores that exact entry's position;
- two entries with the same URL are independent, which is the counterexample itself;
- a canonical REPLACE keeps the same entry and TRANSFERS its stored position to the new
  URL (this also fixed a real bug: replace used to stamp the high-water index, which
  could give two entries the same index);
- a hard reload restores the current entry, because the index survives in
  `history.state`;
- storage is bounded at 30 entries, evicted oldest-first.

A user wheel/touch/key still cancels a pending restoration, and a background refresh
never invokes navigation restoration at all — only a real navigation does.

Acceptance: `lib/scrollRestore.test.ts`, 16 tests, including the exact A-B-A-Back-Back
counterexample asserting 800, the canonical-replace transfer, the discarded-forward
case and the bound; plus the same counterexample as a real browser test in
`e2e/place.spec.ts`. The existing async-content settling tests are unchanged.

#### E. Ledger correction

The second-correction-pass entry in section 0 recorded its synchronized base as
`eb1482ff`. That was the base of the EARLIER sessions; the real base for the session
starting at `2efeb9ef` was `a56b69e3`. Corrected in place. The earlier entries that
genuinely used `eb1482ff` are untouched.

### Fourth correction pass: a reference is an occurrence, not a label

Starting HEAD: `759845ca`. Exact-SHA CI was green there. The review of that HEAD found
two things; this half of the ledger records the first: **a lock lookup answered a
question about one declared reference with an authoritative fact about a different
one.**

The third pass had already established that a Product reference link may only be drawn
from evidence of immutable identity -- a digest or content hash `pacto.lock` recorded
when it really resolved, pulled and hashed the bundle. What it did not establish is that
the recorded identity used is the one recorded FOR THIS DECLARED REFERENCE.

The counterexample, now `TestRefOccurrence_TransitiveConfigDoesNotAnswerForADirectReference`
and its policy twin:

- `app` declares config `foo` -> `child-a`, and config `settings` -> `bundle-y`.
- `child-a` declares config `settings` -> `bundle-x`.
- `pacto.lock` holds the TRANSITIVE closure, so it carries two config references named
  `settings`: `child-a`'s (digest X) and `app`'s (digest Y).
- Projecting `app`'s own `settings` asked `Lock.Reference("config", "settings")`, which
  returned the FIRST match. `app`'s settings rendered as a confident canonical link to
  `bundle-x` -- a bundle `app` never referenced.

Both digests are real and each is authoritative; neither is corrupt. The defect is that
`(Kind, Name)` is a LABEL, and the closure is exactly the place where a label is not
unique. A wrong-but-plausible link is worse than an unknown one, because the reader has
no way to tell it apart from a right one.

The closure BUILDER carried the mirror image of the same mistake: it deduplicated by the
declared ref TEXT. A root and a child both declaring `./config` produced one entry,
pinned to whichever directory the walk reached first, and the other occurrence never
appeared in the lock at all -- so even a correct lookup had nothing to find. A relative
ref resolves against the directory of the contract that declared it, so identical text in
two contracts denotes two different bundles.

#### The model: the declaring contract, and nothing more

`lock.Reference` gains ONE field. `From` is the closure path of the contract that
declared the reference: `""` for the root contract, otherwise the path of the occurrence
through which the declaring bundle was reached (`config:foo`, `config:foo/policy:limits`).
`From` + `Kind` + `Name` names exactly one declared occurrence, which is the minimum that
makes the association unambiguous. What was NOT added, and why: the repository basename
is not identity; `ReferenceRef.Name` alone is the label that failed; slice order and
sorted order are artefacts of how the file was written, not statements about who declared
what. None of the four is consulted anywhere in the resolution path.

`Lock.Reference` is replaced by `Lock.RootReference(kind, name)`, which answers only from
entries with `From == ""` and never falls back to a transitive namesake. Two entries
claiming the same occurrence contradict each other, so it returns unresolved rather than
picking one. The projection also refuses a lock whose `root` names a different contract
or version -- those entries are someone else's closure -- and says which contract it
actually describes, so the reader can see why the pins are missing.

The walk is still finite and the transitive feature is not weakened. Deduplication moved
off the ref text and onto the RESOLVED bundle: its registry digest, or its resolved
absolute path for a local ref. Every occurrence is emitted; recursion into an
already-walked bundle is what is skipped. Resolutions are memoized per
(directory, ref text), so one bundle reachable by several paths still costs one fetch.

`lockVersion` goes 1 -> 2. A v1 lock still parses and keeps declaring 1 -- it is never
reinterpreted under the new semantics -- but it records nothing attributable, so its
references degrade to unresolved WITH A STATED REASON naming the version and the fix
(`re-run pacto lock`), and `lock --check` reports it stale. An author-pinned
`oci://repo@sha256:...` still resolves under a v1 lock, because that digest is in the
contract, not the lock. `docs/lockfile.md` documents the version table and the degrade
rule; the demo lockfiles were regenerated.

Everything the third pass fixed still holds: the raw authored ref stays visible, a
destination comes only from an actual immutable resolution, no basename or name
inference, same-name services in different domains never cross, absent or ambiguous stays
unresolved, and `ReferencedBy` reads the same authoritative relationships from the other
end. Dependency semantics are untouched.

Acceptance: `pkg/fleet/refoccurrence_test.go` (8 tests: the config and policy
counterexamples, non-colliding references still resolving, a legacy lock degrading, two
entries claiming one occurrence, a lock belonging to another contract, one naming another
version, and an author-pinned ref surviving a legacy lock) and
`internal/app/lock_occurrence_test.go` (6 tests: root and child namesakes staying
distinct, `RootReference` ignoring transitive namesakes, the same relative ref from two
directories resolving to two bundles, the same target twice recorded as two agreeing
occurrences, a cycle terminating on resolved identity, and deterministic regeneration),
plus the version and ordering tests in `pkg/lock/lock_test.go`.

One gap is worth recording because it is structural rather than accidental: the registry
e2e suite is behind a `//go:build e2e` tag, so `go build ./...`, `go vet ./...` and an
untagged `go test ./...` all compile straight past it. It still called the removed
`Lock.Reference`, and only CI found out. It is now read by occurrence -- `policy-q` AS
DECLARED BY `policy-p`, plus an assertion that the root does not own it -- which proves
the transitive walk from the lockfile itself rather than by elimination. Its hand-written
round-trip fixture also moved to `lockVersion: 2`, since push verifies the lock and a v1
lock is stale by definition now. `go vet -tags e2e ./...` is the cheap local check that
would have caught it.

### Fourth correction pass: the presentation system

The second of the two findings: **the Product had two typographic systems fighting each
other, and the loser was the page title.**

The shape is worth naming because it is the mirror image of the third pass. There, the
product answered confidently and wrongly. Here, the SOURCE reads correctly and the
BROWSER paints something else, which is a failure mode no source-reading test can see:

- an undeclared `var()` is not a parse error. It is invalid at computed-value time, so
  the declaration is dropped and the property inherits. `font-size: var(--text-md)`
  against a token nobody had declared is a component asking for a size and silently
  getting its parent's.
- a role class can simply LOSE the cascade to a legacy class on the same element. The
  stylesheet is valid; the class list is the bug.
- a token can mean two different sizes at once. `html { font-size: var(--text-base) }`
  resolved `0.9375rem` against the browser's own 16px and set the rem base to 15px,
  after which the identical token resolved to 14.06px everywhere else.

#### A. One ramp, and roles instead of tags

`--text-md`, `--radius-md` and `--c-accent-border` were referenced by Product
components and declared nowhere. They are declared now, and the size ramp is closed and
annotated with the pixel value each step renders at. The rem base is deliberately NOT
one of the tokens: `html` sets `93.75%`, a percentage of whatever default the reader
configured, so the app scales with the reader instead of pinning itself to a token that
also means something else. `body` then takes the BODY ROLE, so unclassed paragraph copy
and `.t-body` are finally the same size.

Above the ramp sit nine visual ROLES (`page-title`, `section-title`, `subsection-title`,
`body`, `body-2`, `label`, `meta`, `metric`, `code`) as `--role-*` tokens and `.t-*`
classes in `src/styles/typography.css`. The separation is the whole point:

- HEADING LEVEL answers "where does this sit in the outline". It is structural, and a
  nested section legitimately drops from h2 to h3.
- VISUAL ROLE answers "what kind of text is this". It is semantic, and two things in the
  same role look identical whatever level carries them.

`base.css` maps each level to its default role once. A component picks a role class; it
does not pick a font-size. The fourth heading size that used to live inside
`ChangeAnalysisView` (an h2 pushed up to METRIC size, so two stage titles outranked
every other section title in the product) is gone, and so is the mobile heading-shrink
block that pulled the page title down toward its own section titles.

#### B. Progressive disclosure, without losing a fact

Every dense entity page now leads with the state a reader needs before opening anything
and folds the exhaustive material into the one shared disclosure grammar. Nothing was
removed: the Revision page's operations, config values, capabilities, agent tools and
contract metadata are all still there, one interaction away, each with a count and a
one-line summary of what is inside so the closed state still says something.

The default-open policy is INFORMATIONAL rather than uniform, and the guards enforce it
both ways: an error-toned section can never be collapsed shut, and the Service page's
"All revisions" opens by default when nothing is running the service, because in that
case it is the only place its revisions appear.

Hover was removed as a sole access path. `data-tip` paints its words through
`::after { content: attr(data-tip) }` -- not in the accessibility tree, hidden outright
under `@media (hover: none)`. The Product uses `HelpTip`: a real button with an
accessible name, `aria-expanded`, `aria-describedby`, focus-open and Escape-close.

#### C. Browser acceptance, and the three defects it caught that source could not

`e2e/typography.spec.ts` plus the mobile block in `e2e/mobile.spec.ts` measure COMPUTED
styles in real Chromium over eleven canonical routes, sharing `e2e/typographyChecks.ts`
(not a `*.spec.ts`, so it is a helper rather than a collected suite; the two projects
cannot share a file). They assert relationships, never absolute pixels: exactly one
visible page title per route, page title larger than normal body text and than every
other role, section titles above body and small print, subsections strictly below their
parent section but still heavier than body, one size and one weight per role across the
whole sweep, and a strictly descending ramp.

Two details make it honest rather than green. `settle()` waits for the count of
role-classed elements to stop changing, because an entity route paints its h1 from the
URL a beat before its data lands and measuring there passes vacuously. `runAnalysis()`
drives Change analysis to its RESULT state, because scoped to a service the route opens
on a revision picker with almost no typography in it.

It found three things the source-level guards structurally could not:

1. `<h3 class="section-title t-subsection-title">`. The legacy V1 `.section-title`
   (uppercase, `--text-sm`, grey) beat the role class, so "Affected consumers" rendered
   as a micro-label SMALLER than the subsections beneath it. Now `ca-subhead`, and a new
   guard rejects any legacy V1 class sharing an element with a typography role.
2. the Operational graph and Change analysis named themselves with a bare `<h1>`. Both
   LOOKED right, because `base.css` paints an h1 at the page-title role -- and both sat
   outside every role-based check, which is how the two of them were the only canonical
   routes with no measurable page title. A guard now requires the explicit role on every
   Product `<h1>`.
3. the rem-base contradiction in the paragraph above, which no source scan can see at
   all because both readings of the token are literally the same string.

#### D. Visual cognitive walkthrough (requirement 21)

BEFORE and AFTER captures of the real built WASM Product in Chromium, dark and light:
desktop Overview, Service, Revision, Operational target, Change analysis and focused
graph at 1440x1000; mobile Service, Revision and Operational target on Pixel 5. The
nine questions, answered against those pairs:

1. **Can I identify the page title instantly?** BEFORE, no. On the Revision page
   "api-gateway 1.0.0" was set at roughly the size of the section titles below it and
   sat inline after a grey REVISION chip and a digest, so the largest text on screen was
   whichever section came first. AFTER, the title is the unambiguous top of the page on
   every route, and the two routes that had no role-bearing title now have one.
2. **Can I identify top-level sections without reading them?** BEFORE, only sometimes:
   the Overview's own four sections rendered at two different sizes, and Change analysis
   had section titles both above and below its subsections. AFTER, one section
   treatment, one size, one weight, everywhere -- and the browser sweep asserts it
   rather than the eye.
3. **Can I see which blocks are nested?** BEFORE, largely not; nesting was carried by a
   card border and nothing else. AFTER, by a real step in the ramp plus the vertical
   rhythm, so a subsection is visibly subordinate before it is read.
4. **Does a subsection ever visually outrank its parent?** BEFORE, yes, in both
   directions -- a chart title hard-coding the section size onto a level-3 heading, and
   the legacy-class collision pushing a real section title BELOW its own subsections.
   AFTER, no, on any of the eleven routes, and the acceptance fails if it returns.
5. **Is the most important state visible without opening anything?** Yes, and more so
   than before. Compliance, revision-match certainty, evidence freshness, the ownership
   conflict and the "source unavailable" banner are all above the fold and none of them
   is collapsible. What moved behind a disclosure is exhaustive detail, never a warning.
6. **Can I reach every detailed fact that existed before?** Yes. The AFTER Revision page
   is 2111px against 2914px, and mobile 7392px against 11517px, with nothing deleted:
   the difference is entirely closed disclosures, each labelled with its count.
7. **Is explanatory prose competing with actual data?** Less. The prose is still there
   -- the chart explanations are load-bearing on a landing page whose first taxonomy is
   Exact/Inferred/Ambiguous/Unresolved -- but it now sits in the BODY-2 role, one step
   below the numbers, instead of at the same weight as the data.
8. **Are disclosures predictable from page to page?** Yes. One caret, one summary line,
   one count position, one keyboard behaviour, on Service, Revision, Operational target
   and Change analysis alike.
9. **Does the page look like one product?** On the audited screens, yes -- the same
   header grammar, ramp, section grammar, disclosure and count pattern in both themes
   and at both widths.

The walkthrough was not a rubber stamp. Reading the AFTER Change analysis capture beside
its BEFORE showed the `incompatible` verdict badge broken mid-word as "incompatibl" /
"e": the help affordances added to three column headers had widened those columns, and
`td .badge { overflow-wrap: anywhere }` let auto table layout squeeze the verdict column
below the width of its own word. `anywhere` and `break-word` break identically once a
word genuinely exceeds its column, but only `anywhere` removes the word from min-content
sizing, which is what allowed a break with room to spare. The rule is `break-word` now,
which keeps the original anti-bleed guarantee for long free-form tags and fixes the
whole family rather than the one badge that showed it.

Two things were deliberately NOT changed, and are recorded as design debt rather than
defects. On the Services list and Needs-attention pages the summary charts are h2s
carrying the SUBSECTION role with no enclosing section title; restructuring that is IA
work, and the Product design freeze is in force. And the "Confidence" column header now
wraps its help button onto a second line at 1440px; widening the header would re-squeeze
the neighbouring column, and the label is fully legible.

#### E. Guards added

In `src/lib/architecture.test.ts`, under `product design system`: a real token
vocabulary including the three that were missing; no undeclared global token in any
component, view or stylesheet (scoped to the shared families, with a positive and a
negative fixture so the scope is provably neither vacuous nor over-broad); the nine
roles declared exactly once; only declared role classes on Product surfaces; no
`font-size` on a heading selector; the page-title role on every Product `<h1>`; no
legacy V1 class sharing an element with a role; no error-toned section collapsed shut;
no `data-tip` on a Product surface (with the legacy host still using it, so the rule is
a scope rather than a claim the attribute is gone); and `HelpTip` proven to be a button
with an accessible name, an exposed open state, an association to its text, Escape and
focus-open.

## 1. Target product model

The dashboard must answer, in order:

1. What needs attention?
2. Search or select an entity.
3. See its operational situation.
4. Explore its local neighborhood.
5. Navigate to revisions, deployments, evidence, ownership, findings and impact.

Three identities are never flattened (this is the engine invariant the product
must honor end to end):

- Service: the logical software capability (domain-qualified `ServiceKey`).
- Revision: one immutable version of an operational contract (`RevisionKey`).
- Deployment/Target: one observed environment or instance reporting against a
  contract revision (`TargetKey`).

User-facing terminology replaces internal ontology in the primary experience:

| Internal (kept in API/docs/devtools) | User-facing (primary UI)          |
|--------------------------------------|-----------------------------------|
| perspective: services/revisions/targets | View: Services / Revisions / Deployments |
| layer: declared                      | Expected dependencies             |
| layer: observed                      | Observed traffic                  |
| layer: reconciled                    | Differences                       |

## 2. API and DTO design (product-oriented, versioned)

`/api/fleet/snapshot` remains a low-level export/debugging API. The frontend must
NOT consume the full `FleetSnapshot` as its primary contract. New bounded,
versioned, product-oriented APIs are the primary contract. Every new response
carries: schema version, snapshot id, as-of time, completeness, relevant source
health, relevant limitations, stable canonical entity references, human labels,
canonical dashboard routes and explicit bounds/pagination.

The product layer is pure over the existing immutable snapshot: `pkg/fleet`
`Query` already exposes Search/GetService/GetTarget/Graph/Status/Explain with a
`Meta` envelope. The product methods live in `pkg/fleet/product.go` and reuse
those internals. `pkg/fleet` is route-neutral: it returns canonical identities
and route-neutral entity references, never dashboard paths or hrefs. Canonical
navigation hrefs are added at the dashboard/product transport boundary from the
exact canonical key (ADR-2). MCP and other non-dashboard consumers use the same
fleet facts and never receive dashboard URLs.

Product schema version: `pacto.dev/fleet-product/v1`.

Endpoints:

- `GET /api/fleet/overview` (requirement 2.1) — product summary with clickable
  entry points. Every summary item carries a canonical route or filter
  descriptor.
- `GET /api/fleet/entities` (requirement 2.2) — global entity search across
  services, revisions, targets, owners, sources. Stable `EntityRef` rows.
- `GET /api/fleet/neighborhood` (requirement 2.3) — bounded neighborhood with
  product knowledge views (expected / observed / differences), graph-ready node
  and edge DTOs with canonical routes and reconciliation state authoritative
  from the backend.
- `GET /api/fleet/entities/{kind}?key=<key>` (requirement 2.4) — unified entity
  detail envelope for service / revision / target / owner / source. The key is a
  QUERY parameter, not a path segment (see the transport note below); the canonical
  FRONTEND route stays `/fleet/<kind>/:key`.
- `GET /api/fleet/attention` (requirement 2.5) — redesigned attention with rich,
  navigable items.
- `POST /api/fleet/impact` (requirement 2.6) — impact by canonical fleet
  identities with snapshot-mismatch rejection. The raw-ref `GET` stays as the
  advanced compatibility path.

Transport note: the unified entity-detail endpoint is `GET
/api/fleet/entities/{kind}?key=<key>`. The key is a query parameter, not a path
segment, because Go's `net/http` mux decodes `%2F` before routing, which would
split a slash-bearing key (a domain-qualified `ServiceKey` or a `TargetKey`)
across path segments. The canonical dashboard route stays
`/fleet/entities/:kind/:key` (the frontend hash router), built at the transport
boundary by the single route builder in `pkg/dashboard/fleetroute.go`
(`hrefForEntity`); only the HTTP transport uses the query-param form for slash
safety. `pkg/fleet` emits no routes and no `RouteFor*` helper.

Core DTOs (see `pkg/fleet/product.go` for the authoritative definitions).
`pkg/fleet` DTOs are route-neutral; the dashboard transport wraps them and adds
canonical hrefs (ADR-2). Fleet-side shapes:

- `EntityRef{ kind, key, label, secondary, status, explanation, domain, scope, parentService }`
  (no route/href; the transport `EntityRef` adds `href` from the canonical key)
- `ProductMeta` — the `Meta` envelope plus the product schema version.
- `Overview{ meta, summary, attention, recentEvidence, entryPoints }` — source
  health lives once in `meta.sources` (bounded); `Overview` has no `sources` field.
- `EntityList{ meta, total, count, entities }`
- `Neighborhood{ meta, focus, direction, depth, views, nodes, edges, unresolvedDependencies(preview), bounds, truncated }`
- `EntityDetail{ meta, entity, status, service|revision|target|owner|source (exactly one), limitations }`
  — strongly typed discriminated payload; no `map[string]any`.
- `AttentionList{ meta, offset, limit, total, count, truncated, nextOffset, items }`

## 3. Routes and information architecture

This section is AUTHORITATIVE for the shipped IA. The six-item primary
navigation it used to describe (Overview, Services, Operational Graph,
Readiness, Owners, Compare) was superseded by the product-coherence correction
and is no longer what the app renders; it is recorded here only so a reader of
an older revision of this file knows which text to disregard.

**Primary navigation (four workflows, teaching one order — state, inventory,
relationships, change):**

1. Overview
2. Services
3. Operational graph
4. Change analysis

**Secondary / contextual destinations** (reached from where they are relevant,
never from the primary nav on a Fleet host):

- Needs attention — `#/fleet/attention`, reached from the Overview tiles and
  from the lead attention number.
- Owners — `#/fleet/owners`, reached from the Overview and from the owner shown
  on any service / revision / target.
- Data sources — `#/fleet/sources`, reached from the Overview source-health
  strip.
- Readiness — NOT a destination. It is the `readiness` attention category and a
  section on a Revision: an attention dimension and a revision dimension, which
  are the two definitions that already existed. `#/readiness` canonicalizes into
  the attention view on a Fleet host.

**Canonical change workspace:** `#/fleet/changes/:serviceKey`. Comparison and
impact are ONE workspace, RevisionKey-based end to end.

Stable routes (hash-router encoding, canonical + reversible):

- `/fleet` (overview)
- `/fleet/services` and `/fleet/services/:serviceKey`
- `/fleet/revisions/:revisionKey`
- `/fleet/targets/:targetKey`
- `/fleet/owners` and `/fleet/owners/:ownerKey`
- `/fleet/sources` and `/fleet/sources/:sourceId`
- `/fleet/attention`
- `/fleet/entities/:kind` (scoped inventory: the bounded Entities endpoint by
  canonical key, so a capped preview always has somewhere complete to send you)
- `/fleet/graph` and `/fleet/graph/:entityKind/:entityKey`
- `/fleet/changes` and `/fleet/changes/:serviceKey`

Legacy routes (`#/`, `#/services`, `#/services/:name`, `#/owners`,
`#/owners/:id`, `#/graph`, `#/diff`, `#/impact`, `#/readiness`,
`#/fleet/impact/:serviceKey`) are COMPATIBILITY REDIRECTS on a Fleet host: they
canonicalize into the product IA via a history REPLACE and never mount a legacy
view. On a NON-Fleet host (the offline `pacto doc` single-service export, which
has no Product API) the legacy views are the only UI and are retained
deliberately.

Navigation adds breadcrumbs, meaningful titles, contextual actions, browser
history, back navigation that preserves filters and focus, and global search.

## 4. Component plan (Svelte 5)

Reusable product components (replacing the FleetView monolith): EntityLink,
EntitySearch, EntityIdentity, CopyableIdentifier, Breadcrumbs,
OperationalSummary, SourceHealth, KnowledgeSelector, AdvancedFilterPanel,
ActiveFilterChips, GraphToolbar, GraphLegend, EntityDrawer, RelationshipDetail,
AttentionList, EvidenceSummary, ResponsiveEntityList, ProductEmptyState,
RevisionLinkState.

Responsibilities split: API data loading, route state, neighborhood controls,
graph rendering, entity details, attention, source status, responsive
presentation. Route and identity construction is centralized (never duplicated
across views), consuming backend-provided canonical routes.

## 5. Complete remaining program

This is the durable, whole-program sequence. Each phase is finished only under
the section 9 acceptance criteria before the next begins. Status is tracked in
section 8.

1. Product API hardening (foundation): architectural boundary restored,
   product-query immutability enforced, strongly typed discriminated entity
   detail, genuinely bounded responses with typed page metadata, corrected
   entity-search / neighborhood / impact semantics, a typed frontend product
   API client with drift protection, and the two-dimension identity model
   (revision-match certainty vs content retrievability). COMPLETE (only the
   U+00A7 commit-history CI enforcement remains deferred; see section 8 item 9).
2. Frontend IA and routing: route state, breadcrumbs, history, global search, the
   reusable product components, consuming the typed client. COMPLETE (this is the
   product-IA foundation; see the phase boundary in section 8).
3. Overview, Services, Attention and entity pages (service / revision / target /
   owner / source) built on the typed detail model. COMPLETE.
4. Search-first Operational Graph: neighborhood-oriented topology with the
   knowledge views (expected / observed / differences) and honest focus mapping.
   COMPLETE this session (an actual Cytoscape visual topology; final gate is
   final-SHA CI). See the authoritative current-status section 0a.
5. Responsive and accessible interaction (keyboard, ARIA, focus, mobile). COMPLETE.
6. WASM browser acceptance (Playwright over the in-browser demo). COMPLETE (re-closed):
   every Phase-6 criterion has a spec (see the coverage reconciliation in section 0c),
   including the four added later -- boundedness at scale, hostile identity,
   the composed visualization contract, and recorded render baselines -- and the three
   added by the second correction pass: keeping the user's place across a refresh,
   navigable contract references, and search-as-you-type. This phase was REOPENED after
   a second real-user review of `2efeb9ef`, because it had been closed with a
   `test.fixme` covering a capability the previous dashboard shipped. It may return to
   COMPLETE only with its browser acceptance green and no `fixme` over a required
   existing capability; both now hold. The final gate is final-SHA CI. See the
   authoritative current-status section 0a.
7. Operator-managed trace source: an operator-owned observed/trace source so the
   observed layer is real end to end, not demo-only.
8. Live Kind vertical: the full install (operator + dashboard + Evidence Server +
   registry + reconciled CRs + ingested evidence) with a live browser acceptance.
9. Real MkDocs browser acceptance (bundle-doc mermaid renders in a real browser).
10. Docker Desktop / local-registry Kind support (containerd store, `kind load`
    reproducibility on a developer machine).
11. Multi-root MCP catalog core: the fleet facts exposed as a multi-root MCP
    catalog (route-neutral; consumes the same fleet facts, never dashboard URLs).
12. MCP discovery tools and protocol E2E.
13. Normative invariants: the architecture / invariant test suite covering the
    engine invariants (three identities, declared-vs-observed, snapshot parity,
    boundary, bounds).
14. Documentation and final verification: docs, PR body, final-SHA CI, user UI
    sign-off, PR marked ready.

## 6. Requirement-to-test mapping

| Requirement group | Backend tests | Frontend/browser tests |
|-------------------|---------------|------------------------|
| 2.1 overview      | `pkg/fleet/product_test.go` (Overview*), `pkg/dashboard/product_test.go` | WASM scenarios 1,2,16 |
| 2.2 entities      | `pkg/fleet/product_test.go` (Entities*)                    | scenarios 3,4 |
| 2.3 neighborhood  | `pkg/fleet/product_test.go` (Neighborhood*)               | scenarios 5,6,17,23 |
| 2.4 entity detail | `pkg/fleet/product_test.go` (EntityDetail*)               | scenarios 7,8,9,10,11 |
| 2.5 attention     | `pkg/fleet/product_test.go` (Attention*)                  | scenarios 2,12,13 |
| 2.6 impact        | `pkg/dashboard/impact_product_test.go` (impact POST), `internal/cli/dashboard_impact_e2e_test.go` (real provider) | scenarios 14,15 |
| routes            | `pkg/dashboard/producttransport_test.go` (transport hrefs) | scenario 18 |
| honest state (10) | product Overview/Attention completeness tests             | scenario 16 |
| a11y (12)         | n/a                                                        | scenarios 19,20 |
| responsive (11)   | n/a                                                        | scenarios 21,22 |
| projections (17)  | `pkg/evidencestore/*_test.go`                              | Kind evidence E2E |
| invariants (19)   | `tests/architecture/*`                                     | n/a |
| U+00A7 gate (24)  | `tests/scripts/check_section_test.go`                      | n/a |
| boundedness at scale | `pkg/fleet/product_test.go` (page bounds), `pkg/dashboard/product_test.go` | `e2e/product-scale.spec.ts` |
| hostile identity  | `pkg/fleet/matchrevision_identity_test.go`, `pkg/dashboard/producttransport_test.go` (route escaping) | `e2e/product-scale.spec.ts` |
| visualization system | n/a (presentation)                                      | `src/components/viz/viz.test.ts`, `src/lib/architecture.test.ts`, `e2e/viz-acceptance.spec.ts` |
| render baselines  | n/a                                                        | `e2e/product-scale.spec.ts` (recorded, not gated) |

## 7. Architectural decisions

- ADR-1: Product API layer is pure over the immutable snapshot, in `pkg/fleet`,
  reusing `Query`. Reason: single source of graph semantics; frontend cannot
  invent stronger semantics than backend facts.
- ADR-2: route emission is a TRANSPORT concern, not a fleet concern. `pkg/fleet`
  is route-neutral: it owns canonical identities, graph/query facts, completeness
  and limitations, and returns route-neutral entity references. The
  dashboard/product transport (`pkg/dashboard/{producttransport,fleetroute}.go`)
  wraps those references into navigable API references by adding a canonical href
  built from the exact canonical key via a single route builder, and owns the HTTP
  product DTOs where navigation is required. Reason: emitting `/fleet/...` route
  strings from `pkg/fleet` is semantic UI coupling even with no dashboard import;
  MCP and other non-dashboard consumers use the same fleet facts and never receive
  dashboard URLs. `tests/architecture` `TestFleetStaysRouteNeutral` forbids
  dashboard route concepts from returning to `pkg/fleet`.
- ADR-3: `reconciliation` is a backend fact on the edge; the product
  `differences` view reads it verbatim. Reason: requirement 2.3 authority rules.
- ADR-4: Impact by canonical identities requires the exact immutable content the
  snapshot revisions name (a digest-pinned, internally consistent ref) and rejects
  a mutable reference or a snapshot-id mismatch. Reason: requirement 2.6.
- ADR-5 (projection, requirement 17): **Option A — remove the write-only
  per-target projections.** They were written under
  `materialized/targets/<hash>/latest.json` but read by no serving path, no
  recovery path, no test and no E2E (the in-memory index, rebuilt from the
  immutable log, serves every read). Retaining them was write-only derived state
  with an unverified implied correctness guarantee. Removed the per-target write
  from commit and repair and deleted `targetKeyPath`. The single remaining
  materialized projection is `materialized/manifest.json`, a record-count summary
  that recovery reads back and verifies against the log — honest, verified derived
  state. `RepairProjections` now rewrites only the manifest; the Kind Evidence E2E
  physically proves the manifest is rewritten on disk after loss (not only that
  `/targets` answers from memory). Storage ADR and tests updated; the overstated
  "repairs missing/corrupt per-target projection" claim is gone.
- ADR-6 (frontend/backend wire contract): **Huma/OpenAPI is the single source of
  truth for dashboard HTTP transport. TypeScript request/response types and
  endpoint serialization are generated deterministically from that OpenAPI
  contract. Handwritten frontend code may add behavior and ergonomics, but MUST
  NOT duplicate the wire schema or reconstruct request URLs/bodies
  independently.** This reverses the item-8 decision that kept a hand-written
  `productTypes.ts` mirror plus an in-Go structural drift parser
  (`producttypes_drift_test.go`). Reason: a hand-written TypeScript mirror is a
  third source of wire truth kept in sync by bespoke machinery; it silently
  drifted (`SeverityUnknown` missing from the severity union). Generation makes
  the OpenAPI contract the only wire truth and the TS SDK a pure derivative.
  Chosen generator: `openapi-typescript` (types) + `openapi-fetch` (typed
  transport). Reason: it consumes OpenAPI 3.1, emits strongly typed request and
  response models with typed path/query/body parameters, supports a custom fetch
  transport (needed for the WASM/static seam), carries a tiny (~6 kB) runtime,
  requires no code-gen server, and is deterministic when version-pinned — the
  smallest footprint that meets every requirement, versus Orval's heavier
  React-Query-oriented output. The generated output is COMMITTED under
  `pkg/dashboard/frontend/src/lib/generated/` with a DO NOT EDIT notice and a
  CI drift gate (`make check-dashboard-sdk-drift`): regenerate OpenAPI, regenerate
  the SDK, `git diff --exit-code` the generated artifacts, so a backend schema or
  operation change without regenerated frontend artifacts fails CI. Committed
  output plus a drift gate (rather than generate-on-build) gives reviewers a
  visible contract diff and keeps editor/type-check operations generation-free.
  The generated types must be precise enough to be correct, so finite wire
  vocabularies (severities including `unknown`, entity kinds, statuses, source
  health, knowledge views, difference/link states, directions) are Go-owned enums
  that surface in OpenAPI. A small handwritten facade over the generated client adds
  ergonomics but never redeclares a DTO field or builds an `/api/...` URL: it DERIVES
  every request shape from the generated `operations` and every response type from
  the generated `paths` (so a wire change flows in automatically and no operation is
  typed `unknown`); the only handwritten shapes are deliberate ergonomic refinements
  (array-valued `kinds`/`views`). Because Huma cannot express a nested-discriminator
  `oneOf`, `ProductEntityDetail` is a broad object with all payloads optional, and
  the facade NARROWS it at the boundary: `narrowEntityDetail` validates
  exactly-one-payload-matching-kind at runtime and returns a `NarrowedEntityDetail`
  discriminated union, throwing a typed `ApiContractError` otherwise. The single
  transport seam matches static fixtures by request semantics (method, normalized
  query, body) and fails an unfixtured operation honestly, never with a misleading
  200 + null.

## 8. Completed and pending

Completed:

- (phase 1) durable plan doc (this file).
- (phase 1) product query layer `pkg/fleet/{product,entities,neighborhood,detail}.go`:
  `Overview`, `Entities`, `Neighborhood` (expected/observed/differences),
  `EntityDetail` (service/revision/target/owner/source), `Attention`, all pure
  over the immutable snapshot and ROUTE-NEUTRAL, all 100% covered.
- (phase 1) target self-describes ambiguity: `linkTargets` records the
  `REVISION_LINK_AMBIGUOUS` limitation on the target, so overview and detail
  classify a link exact / inferred / ambiguous / unresolved.
- (phase 1) dashboard product transport `pkg/dashboard/{producttransport,fleetroute,fleet_product}.go`:
  href-bearing DTOs wrapping every route-neutral fleet answer via a single
  canonical route builder; six product endpoints; impact by canonical identity
  with the exact-content invariant; 100% covered; OpenAPI exports the complete
  contract.

- projection architecture decision (ADR-5) is COMPLETE (requirement 17):
  - the unused write-only per-target projections were removed;
  - `materialized/manifest.json` is the ONLY supported materialized projection (a
    record-count summary recovery reads back and verifies against the immutable
    log);
  - the Kind Evidence E2E physically proves manifest reconstruction on disk after
    loss, not merely that reads answer from the rebuilt in-memory index.
  `pkg/evidencestore` stays 100% covered; the storage ADR and Kind E2E are updated.

### Product API hardening (phase 1 of the program) — status

Every DONE item below is committed with tests and holds 100% package coverage;
the full `ci-test` race+coverage gate, `ci-static-engine` (fmt/vet/gocyclo/
golangci-lint/section-gate/docs-drift/ui-drift), `tests/architecture`, the
TS/OpenAPI drift gate, frontend lint+vitest and the cluster-free dashboard E2E
all pass.

1. Restore the `pkg/fleet` architectural boundary. DONE. `route.go`,
   `EntityRef.Route`, `Link`, and every `RouteFor*`/`net/url` route concern are
   gone from `pkg/fleet`; the layer is route-neutral. `EntryPoint` carries a
   route-neutral `(view, category)` descriptor. `tests/architecture`
   `TestFleetStaysRouteNeutral` is an AST invariant that fails if a `/fleet` path
   literal, a `RouteFor*` helper, a `Route`/`Href` field or a `net/url` import
   returns to `pkg/fleet`.
2. Enforce product-query immutability. DONE for Overview, Entities, Attention,
   Neighborhood, EntityDetail (deep terminal clone) and ProductImpact
   (value-built + copied limitations). Deep-mutation regression tests exist per
   family, including every EntityDetail kind and the ProductImpact transport DTO,
   proving the snapshot is untouched and a second identical query reproduces the
   original answer without relying on HTTP JSON serialization.
3. Replace `map[string]any` entity detail with a strongly typed discriminated
   product model. DONE. `EntityDetail` carries exactly one of
   Service/Revision/Target/Owner/Source; OpenAPI expresses concrete structures;
   every nested list is a bounded typed preview.
4. Make every product response genuinely bounded, in BOTH output and work, and
   canonical. DONE. Every collection-bearing product field is a bounded preview with
   truthful total/count/truncated (see the boundedness audit below): ownership
   conflicts (`StringsPreview`), readiness checks (`ProductReadiness` +
   `ReadinessChecksPreview`), and the owner-attention / service-relationship previews
   carry the true total of the paged/bounded result they wrap (never a double-
   truncated page count). Beyond output size, the last correction pass closed the
   work-boundedness and canonicalization counterexamples:
   - Observed runtime is bounded at INGESTION. `TargetRecord.ObservedRuntime` is a
     precomputed `RuntimePreview`, flattened ONCE at Build from the untrusted raw map
     (the single documented unbounded-source pass); the raw map is not retained, so
     no query, clone or snapshot export does work proportional to its width, and the
     product query is O(fixed bound). `pkg/fleet/runtime_bounds_test.go` proves a
     200k-wide source runtime makes the query allocate no more than a trivial one.
   - `ProductFinding` conversion is bounded in WORK, not just output: `productFinding`,
     `findingsPreview` and the attributed-findings aggregation convert only the
     emitted prefix of a (possibly extension-supplied, unbounded) evidence-ref or
     finding slice while reporting the truthful total.
     `pkg/fleet/finding_bounds_test.go` proves conversion allocation stays bounded
     regardless of input width.
   - Finite-enum values are canonical before they enter the product layer. `Build`
     canonicalizes `RawTarget.Compliance`, each finding `Severity` and a source-
     declared `Status` at ingestion (conservatively normalized, usable record kept,
     `SOURCE_RECORD_INVALID` surfaced), so a custom `fleet.Source` can never make the
     runtime emit a value the generated OpenAPI enum forbids.
     `pkg/fleet/enum_ingestion_test.go` (adversarial custom source) and
     `pkg/dashboard/enum_conformance_test.go` (every emitted enum field conforms to
     the generated OpenAPI domain, end to end) prove it.
5. Correct entity-search semantics. DONE (revision-owner discoverability,
   structured owner matching, source-health filter, typed 422 on invalid combos).
6. Correct neighborhood semantics. DONE. Expansion affordances are now derived
   from the same requested knowledge views as the traversal, so an expected-only
   answer never advertises an expansion that exists only in observed knowledge
   (and vice versa). Regression tests cover expected/observed/differences across
   incoming and outgoing directions. True revision/deployment graph projections
   remain a later phase.
7. Correct contextual product impact and the two-dimension identity model. DONE.
   The model tracks TWO orthogonal dimensions, no longer conflated under a single
   "exact identity" classifier:
   - REVISION-MATCH CERTAINTY ("which revision is this target running, and how
     confidently"): `matchRevision` -> a target's `LinkState` of exact / inferred /
     ambiguous / unresolved.
   - CONTENT RETRIEVABILITY ("can Pacto retrieve exactly this content"):
     `fleet.ClassifyContentIdentity(resolvedRef, recordedDigest)` ->
     `RevisionIdentity.Retrievable` + `IdentityClass` (exact / missing-digest /
     mutable / no-ref / local / malformed / digest-mismatch), read by RevisionDetail,
     TargetDetail AND Product Impact eligibility.
   The two are independent: a target with a trusted content digest and no canonical
   ref (or a scheme-less ref embedding that digest -- the k8s operator's shape) is an
   EXACT revision match whose content is NOT resolver-retrievable, and it reports
   `LinkState=exact` with `Retrievable=false` WITHOUT contradiction. Product Impact by
   canonical identity requires the retrievability dimension and therefore still
   rejects such content even when a target matches the revision exactly.
   `matchRevision` reuses the retrievability classifier only to derive the target's
   EFFECTIVE content digest (the classifier's canonical digest for an `oci://`
   digest-pinned ref, or a digest embedded in a non-`oci://` ref cross-checked against
   the recorded digest, or the recorded digest) and to reject a self-contradictory
   identity: a ref embedding a digest CONTRADICTING the recorded one, or a malformed
   or multi-`@` ref, is internally inconsistent, never links exact and surfaces a
   `SOURCE_RECORD_INVALID` limitation through `linkTargets`. `ParseCanonicalOCIRef`
   validates the repository with the same go-containerregistry name grammar the
   production BundleStore uses (proven by a resolver-parse-compatibility test), and
   `internal/cli/dashboard_impact_e2e_test.go` drives the complete real-provider
   impact vertical for the dashboard-stripped, tag and digest input spellings plus the
   mutable / local / inconsistent rejections. `pkg/fleet/ref_test.go` and
   `pkg/fleet/matchrevision_identity_test.go` prove both dimensions and their honest
   divergence (`TestTargetIdentity_ExactMatch_NonRetrievable`), and
   `pkg/dashboard/fleet_product_test.go` proves Product Impact rejects non-retrievable
   content even under an exact revision match
   (`TestImmutableRef_ExactMatchButNonRetrievable`).
8. Frontend/backend wire contract (generated SDK, ADR-6). DONE. Huma/OpenAPI is the
   single source of wire truth; a pinned generator (`openapi-typescript@7.13.0` +
   `openapi-fetch@0.17.0`) emits a committed TypeScript SDK
   (`pkg/dashboard/frontend/src/lib/generated/`); `make check-dashboard-sdk-drift`
   regenerates and diffs it, wired into the blocking static gate. The whole dashboard
   frontend consumes the generated client through one transport seam
   (`transport.ts`) shared by live HTTP, the WASM demo and the static export. The
   hand-written `productTypes.ts` mirror and the in-Go structural drift parser are
   gone; the generated SDK is the only wire truth. The last correction pass made the
   thin facade preserve that contract instead of quietly re-erasing it:
   - Request shapes are DERIVED from the generated `operations`. The
     fleetEntities / fleetNeighborhood / fleetAttention / fleetImpactByIdentity
     inputs inherit every wire field automatically; only the array-valued
     `kinds`/`views` are an explicit ergonomic refinement (comma-joined on the wire).
   - Response types are DERIVED from the generated `paths`. No dashboard backend
     operation is typed `Promise<unknown>` any more; every legacy method preserves
     its generated response type.
   - Product entity detail leaves the facade NARROWED. `api.fleetEntityDetail`
     returns `NarrowedEntityDetail` via a facade-level `narrowEntityDetail` runtime
     validator (exactly one payload matching `entity.kind`, no contradictory
     payload), throwing a typed `ApiContractError` on violation. Every payload TYPE
     stays derived from the generated schema.
   - The static transport is request-semantic: it matches a fixture by method +
     normalized query + body (order independent), and an unfixtured operation fails
     honestly with a 501 the facade turns into an `ApiError` (no universal 200 + null
     fallback); `pkg/doc` emits explicit request-semantic fixtures for the offline
     single-service export.
   Compile-time tests in `api.typetest.ts` (svelte-check, threshold error) prove the
   request-type derivation, that no method returns `unknown`, and that entity detail
   is narrowed; `api.test.ts` proves the runtime narrowing and the request-semantic
   transport (method/query/body sensitivity, honest failure, offline route set); and
   `architecture.test.ts` guards that raw network access (bare/qualified/optional-
   chained `fetch`, plus `new XMLHttpRequest`/`EventSource`/`WebSocket` and
   `.sendBeacon`) and hand-built backend paths (`/api/*`, `/health`, `/metrics`,
   segment-anchored so `/metrics-overview` is not a false positive) appear only in the
   transport/facade, that static fixtures are request-semantic, and that no hand-written
   wire DTO mirror returned. This text scan is best-effort defense-in-depth; a dynamic
   alias or bracket-access spelling can still evade it, so a lint-level no-restricted
   rule is the durable enforcement -- a deliberate follow-up, and NOT a Phase-2 blocker
   (Phase 2 keeps consuming the generated SDK through the one transport seam, which is
   what the guard protects).
9. Complete U+00A7 enforcement. Gate capability DONE (the script scans authored
   files, committed generated docs, `--commits base..HEAD` messages and `--text`
   PR title/body, with fixtures per failure mode; the authored-file scan is
   blocking in CI). Commit-history + PR CI enforcement is BLOCKED on explicit
   history-rewrite authorization: 36 of the 98 `base..HEAD` commit messages carry
   a section sign, so wiring `--commits` into blocking CI would make the shared
   branch permanently red until those messages are rewritten (a destructive
   force-push the harness blocks without explicit user authorization). This is the
   one remaining, deliberately-deferred action.

### Phase 1 completion (final state)

Phase 1 (product API hardening) is COMPLETE. Against the section 9 acceptance
criteria, in the final state after the last correction pass:

- every product response is bounded in OUTPUT and in WORK, and canonical: bounded
  previews with truthful totals (ProductFinding evidence refs, RuntimePreview,
  ServiceDetail relationships, Overview previews); observed runtime is bounded at
  ingestion (the product query is O(fixed bound)); ProductFinding conversion touches
  only the emitted prefix; and every finite-value field is canonicalized at ingestion
  so no out-of-schema enum can escape;
- identity is modeled as TWO orthogonal dimensions, not one: revision-match
  certainty (`matchRevision` -> `LinkState`) and content retrievability
  (`ClassifyContentIdentity` -> `RevisionIdentity.Retrievable` + `IdentityClass`, read
  by RevisionDetail, TargetDetail and Product Impact). They may differ honestly (an
  exact match to non-retrievable content) but never disagree dishonestly; a digest/ref
  disagreement is an inconsistency that is never exact. `ParseCanonicalOCIRef`
  validates the real OCI grammar;
- OpenAPI expresses the finite vocabularies consumers need (severities including
  `unknown`, kinds, statuses, source health, knowledge views, difference/link
  states, identity classes, directions), verified by `openapi_enum_test.go` on the
  SPECIFIC schema fields, and by an end-to-end HTTP enum-conformance test;
- the generated TypeScript SDK is the frontend/backend wire contract, the whole
  dashboard frontend consumes it (one facade over the generated client, one
  transport seam for live/WASM/static). The facade DERIVES request types from the
  generated operations and response types from the generated paths (no
  `Promise<unknown>`), narrows product entity detail at the boundary, and matches
  static fixtures by request semantics. No manual TS DTO mirror or in-Go structural
  parser remains, and SDK regeneration is deterministic and CI-blocking;
- `pkg/fleet`, `pkg/dashboard`, `pkg/doc` and `internal/fleetsrc` hold 100%
  coverage; the full race+coverage gate, golangci-lint, gocyclo, architecture, OCI
  and real-provider impact vertical, plus svelte-check + the vitest suite (including
  the facade compile-time and static-transport tests), all pass.

The only remaining deferred item is the U+00A7 commit-history + PR-metadata CI
enforcement (section 8 item 9), still BLOCKED on explicit history-rewrite
authorization; it was not performed this pass.

### Phase boundary: Phase 2 DONE, Phase 3 COMPLETE, Phase 4 COMPLETE

Phase 2 (frontend IA and routing -- the product-IA foundation) is DONE. Phase 3
(product lists, rich per-kind entity pages and the complete attention workflow) is
COMPLETE (closed with adversarial tests; see "Phase-3 closure" below). Phase 4 (the
search-first Operational Graph redesign) is COMPLETE: the first pass shipped a
relationship-browser prototype; the independent review of `540cf692` reopened it, and
this session closed every projection-semantic and visual-graph blocker and made the
graph an actual Cytoscape topology (see "Phase 4 completion" below and the authoritative
current-status section 0a). For the single current truth, read section 0a; the
paragraphs below are historical narrative of the earlier Phase-4 pass.

### Phase-3 closure (this session)

An independent review of `6f7cb1a3` found five correctness gaps and several smaller
inconsistencies. Each was closed with tests that fail before the fix and pass after;
`pkg/fleet`, `pkg/semver` and `pkg/dashboard` hold 100% coverage, svelte-check is
error-clean and the full Vitest suite passes.

- A (Product Impact workspace + Compare identity). The Impact workspace no longer
  loads the raw `FleetSnapshot` and no longer calls the legacy `GET /api/fleet/impact`.
  It is product-oriented end to end: canonical `ServiceKey` -> bounded product
  service/revision data (the service `EntityDetail` revisions preview, and when that
  preview truncates, the bounded/pageable `GET /api/fleet/entities?kinds=revision&
  service=<key>` scope -- never the snapshot) -> canonical `RevisionKeys` ->
  `api.fleetImpactByIdentity(POST)` -> `ProductImpact`. Snapshot-mismatch stays honest
  (a 409 shows a "refresh and retry" affordance); consumers page through the product
  page metadata. A new `EntityFilter.Service` scope (OpenAPI + regenerated SDK) is the
  pageable revision mechanism. Compare's "Analyze impact" CTA no longer passes a
  display NAME as if it were a `ServiceKey`: it resolves the name through the product
  Entities API and offers a canonical `/fleet/impact/:serviceKey` route only for a
  unique match, requires explicit disambiguation for same-named services across
  domains, and never fabricates a route when nothing matches (and offers nothing on a
  non-fleet host). The earlier claim that "Compare now launches the contextual Product
  Impact workspace" was OVERSTATED before this correction and is retracted.
- B (bounded-preview unknown totals). `PreviewSection` now distinguishes an EXACT
  KNOWN total from an UNKNOWN one and never synthesizes a total from count, scanned,
  page size or neighborhood bounds. A truncated service-relationships preview whose
  total is absent no longer renders "X of X" (it says "Showing N. More exist; total
  unknown"); a `RuntimePreview` with an absent total and a present `scanned` never
  presents `scanned` as the total. Every caller was audited; the three synthesizing
  callers (service relationships, revision dependencies, target observed runtime) now
  pass the raw backend total.
- C (canonical revision Previous/Next). `siblingRevisions` no longer orders by
  lexical `RevisionKey` (content-digest order). It orders by semver chronology (via
  the reused `pkg/semver` primitive, extended with an ascending `Compare`), with
  non-semver/missing versions sorted deterministically after semver and the immutable
  content digest used ONLY as a tie-breaker -- so 1.9.0 < 1.10.0 < 2.0.0, prereleases
  order correctly, and changing a content digest never reorders distinct versions.
- D (empty + filtered-incomplete knowledge). `snapshotKnowledge` models the backend
  `empty` completeness as its own level that is NOT incomplete (a fully-understood
  empty fleet is not "knowledge unavailable"), and `filtered-empty` now carries the
  snapshot knowledge so Services / Attention / Owners / Sources still surface the
  incompleteness caveat while showing "no matching records". The Overview no longer
  says "some sources are degraded" merely because completeness is `empty`.
- E (product-list request races). The four product-list views (Services, Attention,
  Owners, Sources) share one reusable `createProductLoader`: a single logical initial
  request (no `onMount` + reactive-effect double fire) and a monotonic generation
  token so an older in-flight response can never overwrite a newer route/filter/refresh
  and destroy invalidates any pending response. Deterministic race tests prove each
  guarantee.
- F1 (services route params). The inert `scope`/`source` params (scope is
  target-only in the Entities API; source was never wired into the Services list) are
  removed from the product Services route state and its URL builder.
- F2 (rich revision payload). The revision page renders the already-available bounded
  ownership, readiness checks, tools, skills and docs as honest previews instead of
  bare count badges.
- F3 (owner canonical identity). The owner attention filter/action is built from the
  canonical owner key, not the display label.

Phase-3 acceptance is recorded honestly (requirement G): the component/deterministic
acceptance (Vitest + svelte-check) is COMPLETE; the richer multi-entity WASM/browser
acceptance (same-named services across domains, ambiguous targets, multi-page
pagination, the full Product Impact vertical in a real browser) is DEFERRED to Phase 6
(WASM browser acceptance). This is not a Phase-4 blocker. The current GitHub Code
Quality review threads are against generated/minified Mermaid vendor assets under
`pkg/dashboard/ui/assets/` and are NOT source-level Phase-3 blockers; they are recorded
for final review-thread cleanup and generated vendor assets are never hand-edited.

Phase 2 -- DONE:

- Product IA and the route foundation. `/fleet` is the operational overview; the
  legacy operational graph moved to `/fleet/graph` (fleetUrl repointed, so Navbar
  and FleetView follow). Product routes: `/fleet/services` (product service list),
  `/fleet/<plural>/:key` (unified entity detail), `/fleet/attention` (paged),
  `/fleet/impact/:serviceKey`, focused `/fleet/graph/:kind/:key`. `parseFleet`
  mirrors the backend route builder (fleetroute.go); keys are percent-escaped path
  segments that round-trip slash-, percent-, OCI- and domain-qualified identities.
  All fleet URL construction is centralized (`hashForHref` adopts the authoritative
  backend `ProductRef.href`; `fleetEntityUrl`/`fleetServicesUrl`/`fleetGraphFocusUrl`/
  `fleetAttentionUrl`/`fleetImpactUrl`/`fleetOverviewUrl` build the same paths from
  (kind, key)); no component assembles a `/fleet/...` string inline. A
  backend-href/frontend-router contract test (both ends) proves every canonical href
  class fleetroute.go emits resolves to its intended destination and none silently
  falls through to the overview.
- Operational Overview (`/fleet`) consuming `/api/fleet/overview` as the sole
  contract (never the snapshot), and honest about an empty fleet (a zero-service
  summary is never rendered as "All clear").
- Truthful knowledge state (requirement H). `lib/knowledgeState.ts` is the single
  reusable decision: `snapshotKnowledge`, `decideViewState` and `allClearAllowed`.
  A partial/degraded snapshot can never render a blanket all-clear.
- Global entity search (`EntitySearch`) querying `/api/fleet/entities` -- discovery,
  not a preloaded list -- disambiguating same-named entities, respecting backend
  bounds, and immune to the stale-request race (a response updates the UI only while
  it belongs to the active search). On fleet-capable hosts the visible Search
  affordance and `/` open it; Cmd/Ctrl-K opens the command palette.
- A minimal useful unified entity route (`FleetEntityView`) consuming the
  entity-detail endpoint as `NarrowedEntityDetail`.
- Foundational reusable components: EntityLink, EntityIdentity, CopyableIdentifier,
  ProductEmptyState, SourceHealth, OperationalSummary, ActiveFilterChips, Breadcrumbs,
  and `lib/entityLabels.ts`.

Phase 3 -- COMPLETE (delivered this program; see the Phase-3 closure above):

- Product Services list (`/fleet/services`) consuming
  `/api/fleet/entities?kinds=service`, the canonical Navbar Services destination.
- Complete Attention workflow -- real URL-driven pagination plus triage filters
  (owner/source/severity/status/stale).
- Rich per-kind entity pages (service / revision / target / owner / source).
- Owners product list/page (and Sources) under the product IA.
- Navigation migration of the remaining primary product views (Readiness, Compare)
  so they participate in the new navigation, breadcrumbs and deep-link model, keeping
  their specialized implementations where semantically appropriate.

Phase 4 -- COMPLETE: the search-first Operational Graph redesign. The first pass
shipped a prototype; the independent review of `540cf692` reopened it, and this session
closed the projection-semantic and visual-graph blockers listed in section 0a and made
the graph an actual Cytoscape topology (see "Phase 4 completion").

Deferred as before: the ESLint no-restricted raw-network rule (defense-in-depth
follow-up) and the U+00A7 commit-history enforcement (BLOCKED on history rewrite).

Phase 3 progress (this session), all consuming the product endpoints via the
generated SDK facade (never the FleetSnapshot):

- Product Services list (`/fleet/services`) built on `/api/fleet/entities?kinds=service`
  with owner/status/domain backend filters + search + stable pagination in the URL,
  distinct filtered-empty / empty-fleet / incomplete-knowledge states, and the
  canonical Navbar Services destination (C/A3). DONE.
- Rich per-kind entity pages (D/E/F/G): service, revision (immutable contract version
  with its own content-retrievability dimension), target (two independent identity
  dimensions rendered honestly), owner and source, each composing the reusable
  section components. DONE.
- Product Owners (`/fleet/owners`) and Sources (`/fleet/sources`) list pages;
  primary Owners nav repointed to the product path; a View-all-sources overview
  affordance (G). DONE.
- Entity-relationship breadcrumbs from canonical DTO refs (H). DONE.
- Attention as a full triage workflow: severity/category primary filters, owner/
  source/status/stale-only advanced filters, chips, URL persistence, real backend
  pagination (A2/I). DONE.
- Reusable product components (K): IdentityBadge, PreviewSection, EntityRefList,
  FindingList, LimitationsList, EvidenceList, RelationshipList. DONE.
- Phase-2 residual counterexamples A1-A6 closed as a preflight. DONE.
- Readiness/Compare migration (J): both stay specialized and keep their existing
  implementations and participate in the primary nav. Compare's "Analyze impact" CTA
  resolves the service NAME through the product Entities API to a canonical
  `ServiceKey` before offering the Product Impact route (unique match -> canonical
  route; same-named services across domains -> explicit disambiguation; no match ->
  no route), corrected in the Phase-3 closure above. Deeper EntityLink migration of
  the legacy Readiness rows to fleet keys is left as a NON-BLOCKING compatibility
  follow-up (it still needs a legacy-name-to-ServiceKey bridge, the same
  name-is-not-a-key problem the Compare CTA now resolves at the point of use).
- Browser acceptance (L): `e2e/fleet-phase3.spec.ts` covers the product-page journey
  in a real browser (Navbar Services + canonical href, service filter reload/back,
  service->revision/deployment/owner, revision->service, target dual-identity honesty,
  owner->service, source->contributed entity, entity breadcrumbs, attention filter
  deep link). Scenarios the small offline demo cannot exercise (same-named services
  across domains, an ambiguous target, an empty fleet, multi-page pagination, the
  search stale-request race) are covered deterministically by the Vitest suite.

Remaining Phase-3 follow-up (not blocking Phase 4): legacy Readiness row navigation
via fleet EntityLink (a non-blocking compatibility follow-up that still needs a
legacy-name-to-ServiceKey bridge). The per-kind graph projections for
revision/deployment graph views are NO LONGER a Phase-3 follow-up: they are a Phase-4
PREREQUISITE (they must exist in the backend before Phase 4 exposes revision or
deployment graph perspectives), tracked in the Phase-4 plan below.

### Phase 4 first pass (historical): search-first prototype

This records the FIRST Phase-4 pass, which shipped a search-first
relationship-browser prototype. It is superseded by the authoritative
current-status section 0a: the independent review of `540cf692` reopened Phase 4
because the projections violate the knowledge-view invariant, overclaim
observation/target identity, and the frontend is not yet a visual Cytoscape
topology. The completion of those blockers is recorded in "Phase 4 completion"
below once closed. What the first pass delivered:

Backend prerequisite (J) -- COMPLETE:

- `pkg/fleet/projection.go` adds real per-kind graph projections selected by a
  `perspective` parameter (service / revision / target), never recolored service
  nodes. The revision projection draws a revision->revision edge ONLY when the
  snapshot resolved a specific provider revision (a lock whose digest matches a known
  revision), else a revision->service edge, never a fabricated `provider@latest`; a
  revision's dependents are only the revisions that lock its exact content. The target
  projection links (a "runs" edge) to the revision a target runs and depends on that
  revision's SERVICES, and never draws a target-to-target edge (the evidence
  establishes service-to-service dependency, not which concrete provider target served
  the traffic). Both are bounded, deterministically ordered, route-neutral, immutable
  and record an honest observation-scope limitation. `NeighborhoodEdge.Relation`
  (dependency|runs) and the response `Perspective`/`Limitations` are new; OpenAPI + the
  generated SDK are regenerated. `pkg/fleet`/`pkg/dashboard` stay 100% covered, with
  counterexample tests (`projection_test.go`) asserting the no-fabricated-provider and
  no-target-mesh invariants BEFORE the UI consumes the perspectives.

Frontend search-first graph (I/K-T) -- IMPLEMENTED with deterministic acceptance:

- `views/GraphView.svelte` replaces the model-first `FleetView` at `/fleet/graph`
  (view `fleet`). With no focus it renders a DISCOVERY state (search + attention entry
  point + an Expected/Observed/Differences explanation) and loads NO neighborhood and
  NO FleetSnapshot -- never a whole-fleet hairball (K/R). With a focus it consumes the
  product `GET /api/fleet/neighborhood` through the generated SDK (never FleetSnapshot)
  for a bounded local neighborhood.
- Default focused neighborhood (L): depth 1, direction both, views expected +
  differences. DECISION: expected + differences is the default because a newcomer's
  first questions are "what does this depend on / what depends on it, and where does
  intent diverge from observed reality"; observed is one toggle away and absence of
  observation is never treated as absence of runtime use.
- Product controls (M): perspective (service/revision/target), knowledge views,
  direction, depth, Expand and Reset focus, all persisted in the URL (Q) via
  `fleetGraphFocusUrl`; the discovery landing is `fleetGraphDiscoveryUrl`. Expansion is
  a bounded depth increase re-merged by the backend, preserving the projection, views
  and direction (N).
- Difference rendering (O) is backend-authoritative: the edge `difference` value is
  rendered verbatim with a distinct text label and tone (never color-only, never
  inferred from booleans), insufficient observation is explicitly not a failure, and
  "runs" edges are labelled distinctly from dependency edges. Unresolved declared
  dependencies and backend truncation stay visible; a partial snapshot shows the
  incompleteness caveat.
- Quick-inspection (P): selecting a node or edge opens a bounded drawer (identity,
  status, knowledge caveat, canonical links: full detail / focus here / impact; and,
  for an edge, the declared claims, observed provenance and difference) without
  navigating away. The full stable entity page remains the durable destination.
- `lib/graphState.ts` owns the pure URL<->state mapping and the difference/relation
  vocabularies; `lib/router.ts` gains the graph URL model (`views`/`direction`/`depth`
  replace the inert legacy `layer`).

Phase-4 acceptance recorded honestly (requirement S):

- Deterministic component acceptance (Vitest: `graphState.test.ts`,
  `GraphView.svelte.test.ts`) is COMPLETE -- discovery-first/no-hairball,
  product-API-only, difference rendering, insufficient!=failure, unresolved visible,
  truncation visible, direction/depth/perspective URL state, expand-preserves-views,
  node/edge quick-inspect, reset-to-discovery. The backend projection counterexamples
  (`projection_test.go`) are COMPLETE.
- Real browser (Playwright) acceptance is DELIVERED for the critical graph journey:
  the WASM demo suite (`e2e/demo.spec.ts`) now drives the search-first graph
  (discovery -> search -> focus a bounded neighborhood; perspective/direction persisted
  in the URL; partial-knowledge caveat; node quick-inspect drawer with a full-detail
  link; the product Impact workspace analyzing by canonical identity + include-observed),
  and the LIVE Kind smoke (`e2e-live/operational-graph.spec.ts`) drives the same
  search-first journey against the operator-managed dashboard over real HTTP. Fixing
  these surfaced (and fixed) a real demo-transport bug: the in-browser fetch shim did
  not forward POST bodies, so the product Impact POST reached the server empty.
- Still DEFERRED to Phase 6 (the richer browser matrix): the full 25-scenario sweep in
  a real browser -- notably the revision/target PERSPECTIVE browser journeys and a
  synthetic large-fleet browser render. The projection semantics behind them are
  already proven by the backend counterexample tests; only their in-browser exercise is
  deferred.

Migration (T): `/fleet/graph` is now the search-first product graph; the legacy
`FleetView` whole-fleet view and its `fleetGraph.ts` adapter have been DELETED (they
were unrouted and unimported), removing the last source of the inert graph-route
filters.

### Phase 4 completion (this session)

This session closed every blocker the independent review of `540cf692` raised (see the
authoritative current-status section 0a) and made the graph an actual visual topology.

Backend projection semantics (`pkg/fleet/projection.go`, `neighborhood.go`), all with
adversarial counterexample tests in `pkg/fleet/projection_views_test.go` and 100%
coverage held:

- Knowledge-view invariant (A): the revision and target projections now derive
  `wantDeclared`/`wantObserved` from the requested views exactly as the service
  projection does. The revision graph is declared-only (observation is service-scoped),
  so an observed-only revision query returns just the focus and traverses no declared
  edge; a node's expansion affordances are gated on the same view set. The target
  projection gates its declared dependency edges on the declared view; the structural
  runs edge is the target's identity link, shown independently.
- Honest observation scope (B): `dependencyEdge` no longer sets `Observed` from
  `rel.Reconciliation` (which is keyed by the from/to SERVICE pair, so it is
  service-scoped). A fine-grained (revision/target) dependency edge is `Observed=false`
  and carries the service reconciliation as CONTEXT via two new OpenAPI-generated edge
  fields, `ObservationScope` (service|target) and `ServiceCorroboration`
  (matched|expected-not-observed|insufficient); it has no edge-scope `Difference`. The
  target runs edge is a genuine target-scoped observed fact
  (`ObservationScope=target`). Counterexample: service A has revisions v1 (declares B)
  and v2; telemetry observes A->B; the revision projection on v1 reports service
  corroboration matched WITHOUT claiming `v1->B observed`, and v2 gets no B edge.
- Target identity (C): an ambiguous or unresolved target inherits NO revision's declared
  dependencies (it surfaces a `TARGET_REVISION_UNRESOLVED` limitation instead of
  aggregating every revision's deps); a logical dependent is drawn as
  consumer->logical-service, never consumer->concrete-target; the runs edge appears only
  for an authoritative exact/inferred link. The tests that locked in the old fallback
  were replaced.
- Target depth (D): the target projection is one hop by construction and reports
  `EffectiveDepth=1` (a new response field); the frontend disables depth/expand for it
  and shows an effective-depth note, so no URL can pretend target depth 6 was evaluated.

Frontend (E-K, N, P):

- Actual visual topology (F/G/H): `views/GraphView.svelte` renders the bounded
  neighborhood as a real Cytoscape graph via the shared engine (`lib/graph.ts`
  `renderGraph`/`buildElements`/`cyLayout`/`cyStylesheet`), a pure adapter
  (`lib/neighborhoodGraph.ts`, a graph node for every returned node, mixed
  service/revision/target kinds, dependency vs runs edges) and a thin wrapper
  (`NeighborhoodGraph.svelte`). Additive, non-breaking engine hooks were added:
  id-based `onSelectNode`/`onSelectEdge`, a `fit()` control, an `edgeStyle:'visible'`
  mode and `autoSpotlightFocus:false` for a bounded neighborhood; the whole-fleet
  consumers are unchanged. The toolbar has perspective/knowledge/direction/depth/expand
  plus Fit/zoom-in/zoom-out/reset; a legend explains node kinds + dependency/runs +
  difference/corroboration/insufficient (shape + dash + label, never color alone);
  node/edge selection opens a quick-inspection drawer without navigating; an accessible
  text alternative lists the same relationships.
- Focus/perspective valid by construction (E): a search result opens its kind's default
  projection; only backend-acceptable perspective transitions are offered (a target
  offers the revision perspective only when its link is authoritative), so ordinary
  navigation cannot produce a 422.
- Inert filters removed (J): the graph route no longer parses/serializes
  domain/scope/owner/status/source/freshness; the dead legacy FleetView/fleetGraph stack
  is deleted.
- Product-honest search (K): a transport/schema failure is shown as an error, never as
  "no matches"; stale responses cannot overwrite a newer query; truncation is surfaced;
  results are graph-focusable kinds only.
- Product Impact selectors (L): the revision selectors page the most recent revisions
  (bounded, stopping at a selector bound) and report an honest incomplete state instead
  of claiming a complete universe (L1); the no-service picker is search-first and
  truncation-aware so a service beyond the first page is discoverable (L2).
- Accessibility boundary (P): the Cytoscape canvas has an accessible label, controls are
  labelled buttons, the text alternative is a meaningful semantic representation, and the
  drawer close/focus basics are preserved. Detailed keyboard graph navigation and mobile
  layout remain Phase 5.

Acceptance:

- Deterministic: `pkg/fleet` and `pkg/dashboard` hold 100% coverage; the projection
  counterexample suite (`projection_views_test.go`, requirement M) passes; svelte-check
  is error-clean; the full Vitest suite passes, including the new
  `lib/neighborhoodGraph.test.ts` (adapter), the rewritten `GraphView.svelte.test.ts`
  (requirement N) and the new `graphState`/`ImpactView` cases; OpenAPI + the TypeScript
  SDK are regenerated deterministically and the new enum fields are pinned in the OpenAPI
  contract test.
- Real browser (Playwright, requirement O): the WASM demo suite proves the CORE visual
  graph journey -- discovery has zero topology nodes; search->focus renders an actual
  Cytoscape topology with nodes/edges/legend; Fit/zoom operate on the canvas without
  navigating; node and edge selection open the drawer; a deep link survives reload;
  browser back restores prior state; a knowledge-view switch changes the requested
  topology; a revision result opens a real revision projection; and a target result
  renders a target + runs relation with the one-hop controls disabled and no fabricated
  mesh. The demo's payments-service target was given a version-pinned ref so its link is
  inferred (authoritative), proving the target/revision projections in-browser. The live
  Kind smoke asserts the focused VISUAL-GRAPH view renders over live HTTP -- its canvas
  controls (Fit/Reset) and the perspective toolbar appear and the neighborhood resolves
  to the drawn topology or the honest empty state; because the live k8s fleet source is
  dependency-light (it carries target/status, not contract dependency edges), the rich
  node/edge/drawer/perspective topology is proven by the WASM demo, not the live smoke.

### Phase-2 IA residual: canonical home + demo entry (this session)

Two small product-IA entry-point corrections (NOT a new phase and NOT a reopening of
Phase 2).

Demo entry point (the Fleet-capable WASM Live Demo must enter through the product
Operational Overview, not the legacy landing): the demo bootstrap
(`examples/demo/boot.js`, a classic script that runs BEFORE the Svelte app module)
canonicalizes a no-meaningful-hash entry (bare `/demo/`, `#`, or the legacy `#/`) to
`#/fleet` before the first render, so the demo never flashes the legacy landing. It is
DEMO-SPECIFIC (boot.js ships only with the demo) and capability-safe -- a generic
non-Fleet dashboard is never forced to assume Fleet -- and it preserves any explicit
deep link (`#/fleet/graph`, `#/fleet/services/<key>`, `#/readiness`, ...). The public
"Live Demo" CTAs (README, `docs/examples/dashboard-demo.md`) now link the canonical
`/demo/#/fleet`. The primary Live Demo entry acceptance starts Playwright at the REAL
no-hash demo URL and asserts it lands on `#/fleet` with the Operational Overview
visible; deep-link preservation and the canonical docs links are covered too. The
legacy `#/` root is retained as the non-Fleet compatibility surface.

Home affordance: the
Pacto brand/logo in `Navbar.svelte` hardcoded `href="#/"`, sending a fleet-capable user
back to the legacy landing instead of the canonical Operational Overview. The brand is
the application HOME affordance, so it now uses the centralized route builder
`fleetOverviewUrl()` under the SAME capability policy as the Services destination: the
fleet Overview (`#/fleet`) once the fleet capability is confirmed, and the legacy `#/`
landing when fleet is explicitly unavailable OR while capabilities are still unresolved
(so the logo is never a dead route). It does not construct a second copy of the fleet
route. An audit of the other `#/` references found only deliberately-retained legacy
back-links inside legacy views (ServiceDetailView/Readiness/Owners/Diff), which are not
the product HOME/logo affordance and are left intact; `#/` remains the intentional
non-fleet compatibility surface. Deterministic Navbar tests prove the brand points to
`#/fleet` when fleet-capable, `#/` when not (and while unresolved), and that the brand
agrees with the Overview nav item; a WASM Playwright assertion proves clicking the logo
from inside the fleet product lands on the Operational Overview.

### Product response boundedness audit (requirement, item 4)

Every collection-bearing field reachable from a product response was audited from
the exported OpenAPI (the 57-schema closure of the six product roots). Result:

- No `map`/`additionalProperties` field is reachable from any product response;
  the only maps in the whole schema set (`FleetSnapshot.{services,revisions,
  targets}`, `*Record.labels`) belong to the low-level `/api/fleet/snapshot` export
  and to record types that NO product response references (class C).
  `TargetRecord.observedRuntime` is no longer a raw map at all: it is a precomputed
  bounded `RuntimePreview`, flattened once at Build from the untrusted source map,
  so even the low-level snapshot export never carries the arbitrarily wide raw map.
- Every product array is one of:
  - Class A (hard cardinality bound with explicit truncation metadata): every
    `*Preview.items` (cap `MaxDetailPreview`, or the per-edge caps
    `MaxEdgeDeclaredClaims`/`MaxEdgeObservationSources`, or
    `MaxUnresolvedDependencies`); every `*Page.items` (`MaxAttentionLimit`,
    `MaxImpactConsumers`); `EntityList.entities` (`MaxEntityLimit`);
    `Neighborhood.{nodes,edges}` (`MaxNeighborhoodNodes`/`Edges`, `Truncated`);
    `ImpactConsumer.path` (`MaxImpactPath`, `pathTotal`/`pathTruncated`);
    `ProductMeta.{sources,limitations}` (`MaxMetaSources`/`MaxMetaLimitations`,
    `sourcesTruncated`/`limitationsTruncated`); `Overview.{attention,
    recentEvidence}` (fixed 10). The four previously-unbounded nested structures
    are now class A: ownership conflicts (`StringsPreview`), readiness checks
    (`ReadinessChecksPreview`), observed runtime (`RuntimePreview`, bounded on nested
    size AND on work - precomputed once at ingestion, never re-flattened at query),
    and the owner-attention / service-relationships previews (which now carry the
    true total and truncation of the paged/bounded result they wrap).
  - Class B (intrinsic small fixed maximum): `Neighborhood.views` and
    `EntityDetail.actions` and `Overview.entryPoints` (fixed vocabularies);
    `NeighborhoodNode.expansions` (at most the two directions).
- The previously-residual `finding.Finding.EvidenceRefs` case is now RESOLVED
  (generated-SDK session): the product transport no longer exposes raw
  `finding.Finding`. Every findings-bearing product response carries a bounded
  `ProductFinding` whose `EvidenceRefs` is an explicit `ProductEvidenceRefsPreview`
  (cap `MaxEvidenceRefsPreview`, truncated preview), so an untrusted extension
  source can never smuggle an unbounded per-finding evidence list. The raw
  `finding.Finding` remains on the low-level snapshot export only. There is now no
  nested product collection whose bound is external rather than an explicit
  truncated preview.

Graph projections (Phase-4 prerequisite J) are now IMPLEMENTED: the neighborhood API
projects real service, revision and target graphs selected by a `perspective`
parameter (see the Phase 4 progress section above and `pkg/fleet/projection.go`). The
earlier note that "true revision-graph and deployment-graph projections are NOT
implemented" is superseded.

Pending after phase 2 (frontend IA/routing, now complete): phases 3 through 14 of
the complete remaining program in section 5 (Overview/Services/Attention/entity
pages; search-first Operational Graph; responsive + a11y; WASM browser acceptance;
operator-managed trace source; live Kind vertical; real MkDocs browser
acceptance; Docker Desktop/local-registry Kind support; multi-root MCP catalog
core; MCP discovery tools + protocol E2E; normative invariants; documentation and
final verification).

## 9. Acceptance criteria

Each phase is accepted only when: its packages are green (100% coverage held),
its tests exist and pass, the U+00A7 gate is green, the increment is committed
and pushed, and this ledger is updated. The PR stays draft until the entire
program is finished, final-SHA CI has completed, and the user has reviewed the
final UI (or waived that sign-off).
