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
those internals. Canonical routes are deterministic strings owned by the fleet
layer (`pkg/fleet/route.go`) so the frontend never re-derives identity or routes.
Routes are plain path strings (the frontend hash-router prepends `#`); emitting a
string introduces no dashboard/k8s import, so the architecture boundary holds.

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
- `GET /api/fleet/entities/{kind}/{key}` (requirement 2.4) — unified entity
  detail envelope for service / revision / target / owner / source.
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
`/fleet/entities/:kind/:key` (the frontend hash router), and `RouteForEntity`
emits it; only the HTTP transport uses the query-param form for slash safety.

Core DTOs (see `pkg/fleet/product.go` for the authoritative definitions):

- `EntityRef{ kind, key, label, secondary, status, explanation, domain, scope, route, parentService }`
- `ProductMeta` — the `Meta` envelope plus the product schema version.
- `Overview{ meta, summary, sources, attention, recentEvidence, entryPoints }`
- `EntityList{ meta, total, count, entities }`
- `Neighborhood{ meta, focus, direction, depth, views, nodes, edges, bounds, truncated }`
- `EntityDetail{ meta, entity, summary, status, sections, relationships, findings, evidence, ownership, limitations, links, availableActions }`
- `AttentionList{ meta, total, count, items }`

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
   API client with drift protection, and complete U+00A7 enforcement. This is
   the current phase; it must be finished before any UI migration begins.
2. Frontend IA and routing: route state, breadcrumbs, history, global search,
   the reusable product components, consuming the typed client. No UI redesign
   before phase 1 is complete.
3. Overview, Services, Attention and entity pages (service / revision / target /
   owner / source) built on the typed detail model.
4. Search-first Operational Graph: neighborhood-oriented topology with the
   knowledge views (expected / observed / differences) and honest focus mapping.
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
| 2.6 impact        | `pkg/dashboard/product_test.go` (impact POST)             | scenarios 14,15 |
| routes            | `pkg/fleet/route_test.go`                                  | scenario 18 |
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
- ADR-2 (corrected): route emission is a TRANSPORT concern, not a fleet concern.
  `pkg/fleet` stays route-neutral: it owns canonical identities, graph/query
  facts, completeness and limitations, and returns route-neutral entity
  references. The dashboard/product transport converts those references into
  navigable API references (adding canonical hrefs/routes) and owns the HTTP
  product DTOs where navigation is required. Reason: emitting `/fleet/...` route
  strings from `pkg/fleet` is semantic UI coupling even with no dashboard import;
  MCP and other non-dashboard consumers must use the same fleet facts without
  receiving dashboard URLs. This supersedes the earlier ADR-2 (routes emitted
  from `pkg/fleet/route.go`); an architecture test now forbids dashboard route
  concepts from returning to `pkg/fleet`.
- ADR-3: `reconciliation` is a backend fact on the edge; the product
  `differences` view reads it verbatim. Reason: requirement 2.3 authority rules.
- ADR-4: Impact by canonical identities resolves revision keys to their snapshot
  refs and rejects a snapshot-id mismatch. Reason: requirement 2.6.
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

## 8. Completed and pending

Completed:

- (phase 1) durable plan doc (this file).
- (phase 1) canonical route layer `pkg/fleet/route.go` (100% covered).
- (phase 1) product query layer `pkg/fleet/{product,entities,neighborhood,detail}.go`:
  `Overview`, `Entities`, `Neighborhood` (expected/observed/differences),
  `EntityDetail` (service/revision/target/owner/source), `Attention`, all pure
  over the immutable snapshot, all 100% covered.
- (phase 1) target self-describes ambiguity: `linkTargets` now records the
  `REVISION_LINK_AMBIGUOUS` limitation on the target too, so overview and detail
  can classify a link exact / inferred / ambiguous / unresolved.
- (phase 1) dashboard HTTP wiring `pkg/dashboard/fleet_product.go`: six product
  endpoints (`/overview`, `/entities`, `/entities/{kind}`, `/neighborhood`,
  `/attention`, `POST /impact`) with impact enrichment and snapshot-mismatch
  rejection, 100% covered; OpenAPI exports cleanly.

- projection architecture decision (ADR-5) is COMPLETE (requirement 17):
  - the unused write-only per-target projections were removed;
  - `materialized/manifest.json` is the ONLY supported materialized projection (a
    record-count summary recovery reads back and verifies against the immutable
    log);
  - the Kind Evidence E2E physically proves manifest reconstruction on disk after
    loss, not merely that reads answer from the rebuilt in-memory index.
  `pkg/evidencestore` stays 100% covered; the storage ADR and Kind E2E are updated.

Correction (stale entry removed): an earlier ledger version claimed a "typed
product API client" was delivered. That was overstated. The client in
`pkg/dashboard/frontend/src/lib/api.ts` (`fleetOverview`, `fleetEntities`,
`fleetEntityDetail`, `fleetNeighborhood`, `fleetAttention`,
`fleetImpactByIdentity`) exists and has vitest coverage, but its methods return
`Promise<unknown>` with no product DTOs, no schema-version check and no drift
protection. Producing a genuinely typed client with drift protection is part of
phase 1 (product API hardening), not a completed item.

### Product API hardening (phase 1 of the program) — status

This is the current phase. Its subitems and their status:

1. Restore the `pkg/fleet` architectural boundary (route emission moves to the
   transport; `pkg/fleet` becomes route-neutral; an architecture content-scan
   test forbids route concepts from returning to `pkg/fleet`).
2. Enforce product-query immutability (every product answer fully independent of
   the snapshot and of later answers; regression tests per response family).
3. Replace `map[string]any` entity detail with a strongly typed discriminated
   product model; OpenAPI expresses the real fields.
4. Make every product response genuinely bounded (defaults + hard maxima, reject
   negatives, cap positives, typed page metadata; `ProductMeta` not an unbounded
   copy of every source/limitation).
5. Correct entity-search semantics (revision-owner discoverability, structured
   owner matching, source-health enum, typed 422 on invalid kind/filter combos).
6. Correct neighborhood semantics (views drive traversal; explicit difference
   states; per-source-revision declared claims; honest service-neighborhood
   focus; true revision/deployment projections deferred to a later phase).
7. Correct contextual product impact (canonical identity parity, snapshot-refresh
   race rejection with 409, immutable content resolution).
8. Typed frontend product API client with schema-version validation and drift
   protection (client only; UI migration is phase 2).
9. Complete U+00A7 enforcement (gate scans authored files, committed generated
   docs, base..HEAD commit messages, PR title and body; fixtures for each).

Deferred graph projections (recorded so a later phase implements them before the
corresponding UI): this API version is honestly service-neighborhood oriented.
True revision-graph and deployment-graph projections (nodes that are revisions or
targets, not services) are NOT implemented and must be added before the revision
and deployment graph UI views are built.

Pending after phase 1: phases 2 through 14 of the complete remaining program in
section 5 (frontend IA/routing; Overview/Services/Attention/entity pages;
search-first Operational Graph; responsive + a11y; WASM browser acceptance;
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
