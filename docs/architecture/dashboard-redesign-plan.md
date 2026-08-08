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
2. Frontend IA and routing (CURRENT PHASE): route state, breadcrumbs, history,
   global search, the reusable product components, consuming the typed client.
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

### Phase 2 progress (frontend IA and routing)

Phase 2 (frontend IA, routing and the Operational Overview) is IN PROGRESS. The
foundation is landed and unit-tested; the rich per-kind entity pages and the
search-first graph redesign remain for Phase 3/4.

DONE this pass:

- Route model + centralized navigation. `/fleet` is the operational overview; the
  legacy operational graph moved to `/fleet/graph` (fleetUrl repointed, so Navbar
  and FleetView follow). New product routes: `/fleet/<plural>/:key` (unified entity
  detail), `/fleet/attention`, `/fleet/impact/:serviceKey`, focused
  `/fleet/graph/:kind/:key`. `parseFleet` mirrors the backend route builder
  (fleetroute.go); keys are percent-escaped path segments that round-trip slash-,
  percent-, OCI- and domain-qualified identities (proven in `router.test.ts`). All
  fleet URL construction is centralized: `hashForHref` adopts the authoritative
  backend `ProductRef.href`, and `fleetEntityUrl`/`fleetGraphFocusUrl`/
  `fleetAttentionUrl`/`fleetImpactUrl`/`fleetOverviewUrl` build the same paths from
  (kind, key). No component assembles a `/fleet/...` string inline.
- Truthful knowledge state (requirement H). `lib/knowledgeState.ts` is the single
  reusable decision: `snapshotKnowledge` derives quality from meta completeness +
  per-source health (strictest wins), `decideViewState` distinguishes empty-fleet /
  filtered-empty / "nothing known under incomplete knowledge" / backend-error /
  schema-error / not-found, and `allClearAllowed` gates any all-clear on complete
  knowledge with zero attention. A partial/degraded snapshot can never render a
  blanket all-clear.
- Foundational components: EntityLink, EntityIdentity, CopyableIdentifier,
  ProductEmptyState, SourceHealth, OperationalSummary, ActiveFilterChips (plus the
  existing Breadcrumbs), and `lib/entityLabels.ts` (user-facing labels/tones for
  kinds and BOTH identity dimensions -- revision-match certainty vs content
  retrievability -- knowledge levels and source health).
- Operational Overview (`/fleet`) consuming `/api/fleet/overview` as the sole
  contract (never the snapshot); unified entity route (`FleetEntityView`) consuming
  the entity-detail endpoint as `NarrowedEntityDetail`; attention list
  (`FleetAttentionView`) the overview category tiles link to.
- Global entity search (`EntitySearch`), a `/`-opened modal (command-palette
  keyboard conventions) querying `/api/fleet/entities` -- discovery, not a preloaded
  list -- disambiguating same-named entities and respecting backend bounds.

Tested (Vitest): `router.test.ts` (route round-trip incl. slash/percent, builders),
`knowledgeState.test.ts` (state machine + the non-negotiable all-clear rule),
`entityLabels.test.ts`, `productComponents.test.ts` (link/identity/empty-state/
source-health/summary + not-found/schema-error rendering), `FleetOverview.svelte.
test.ts` (scenarios 1-5), `FleetEntityView.svelte.test.ts` (entity success +
identity-dimension rendering + action routing), `EntitySearch.test.ts` (scenarios
6-10, 16).

Tested (real-browser Playwright, `e2e/fleet-ia.spec.ts` against the WASM demo, which
serves the product endpoints): scenario 1 (/fleet loads the overview), 4 (a summary
tile navigates to its exact filtered view), 5 (a degraded source is navigable), 6/8-10
(search finds and opens an entity by canonical identity, any kind), 11+13 (a
deep-linked entity route survives a reload; the encoded key round-trips), 12 (browser
back returns to the overview). The existing demo spec was migrated to /fleet/graph.
The WASM demo dist is rebuilt from source; the committed UI bundle is rebuilt from the
generated-SDK frontend. No physical-device testing is claimed (headless chromium).

REMAINING Phase-2 work:

- Migrate the remaining legacy views (Services list, Owners, Readiness, Compare)
  behind the new navigation, and expand the rich per-kind entity pages (Phase 3).
- The ESLint no-restricted raw-network rule (defense-in-depth follow-up) and the
  U+00A7 commit-history enforcement remain deferred as before.

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
