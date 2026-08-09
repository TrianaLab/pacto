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
U+00A7. Use ordinary wording ("requirement 3", "review item 3"). This rule is
enforced by a blocking gate that scans authored files, committed generated docs,
the commit messages in base..HEAD, and the PR title and body (requirement 24).

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

## 0a. Current status (authoritative)

This is the single authoritative status. Any older phase heading below is
historical narrative; where it conflicts with this section, this section wins.

- Phase 1 (product API hardening): COMPLETE.
- Phase 2 (frontend IA and routing): COMPLETE.
- Phase 3 (Overview, Services, Attention, rich entity pages): COMPLETE.
- Phase 4 (search-first Operational Graph): implementation COMPLETE this session and
  locally verified; the final gate is final-SHA CI (this ledger commit's SHA). The
  independent review of `540cf692` accepted Phases 1-3 and reopened Phase 4 as only a
  search-first relationship-browser prototype; every blocker it raised is now closed
  (details in "Phase 4 completion" below):
  - the revision and target projections now honor the knowledge-view invariant (views
    drive traversal, edges AND expansion affordances);
  - service-scoped observation is never promoted into a revision/target edge claim: a
    fine-grained edge is never `Observed`, and carries `observationScope` +
    `serviceCorroboration` as explicit, OpenAPI-generated context;
  - target identity is honest: an ambiguous/unresolved target inherits no revision's
    dependencies (it surfaces a limitation), a logical consumer depends on the logical
    service (never the concrete target), and the runs edge is drawn only for an
    authoritative link;
  - the target projection is one hop by construction and reports `effectiveDepth=1`; the
    UI disables its depth/expand controls rather than leaving them inert;
  - only backend-acceptable perspective transitions are exposed, so ordinary navigation
    cannot produce a 422;
  - `GraphView.svelte` is now an actual Cytoscape visual topology (the shared engine,
    reused) with Fit/zoom/legend and node/edge quick-inspection drawers, plus an
    accessible text alternative;
  - the inert `domain/scope/owner/status/source/freshness` graph-route filters are
    removed and the dead legacy FleetView/fleetGraph stack is deleted;
  - graph discovery search distinguishes transport/schema failures from "no matches"
    and is stale-safe;
  - the Product Impact revision universe / service picker are bounded and truthfully
    incomplete/search-first.

The projection / materialized-storage work from the earlier evidence-store review
(ADR-5) is resolved and is NOT reopened. The U+00A7 commit-history CI enforcement
remains BLOCKED on explicit history-rewrite authorization (section 8 item 9); no
such authorization exists this session.

Phase 5 (responsive + accessible interaction: keyboard graph navigation, mobile
layout, formal WCAG) has NOT been started and is the next phase.

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

Primary navigation: Overview, Services, Operational Graph, Readiness, Owners,
Compare. Impact is a contextual workflow reached from Compare, service detail,
revision detail, the graph and deep links (it keeps a route but is never the
primary raw-ref form).

Stable routes (hash-router encoding, canonical + reversible):

- `/fleet` (overview)
- `/fleet/services/:serviceKey`
- `/fleet/revisions/:revisionKey`
- `/fleet/targets/:targetKey`
- `/fleet/owners/:ownerKey`
- `/fleet/sources/:sourceId`
- `/fleet/attention`
- `/fleet/graph/:entityKind/:entityKey`
- `/fleet/impact/:serviceKey`

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
5. Responsive and accessible interaction (keyboard, ARIA, focus, mobile).
6. WASM browser acceptance (Playwright over the in-browser demo).
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
