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

The generated-SDK + residual-boundedness session (this ledger's reversal of the
in-Go structural drift gate in favor of a generated TypeScript SDK, plus closing
the last product-response counterexamples) ran as follows:

- Starting HEAD: `b9d1962c` (the reviewed HEAD of PR #291).
- Merge-base with main and current `main` tip are both still the synchronized
  base `eb1482ff` (main has NOT moved since the reviewed base), so no re-sync was
  needed. Integration strategy remains merge (branch content preserved).
- An independent review supplied fresh, concrete counterexamples showing three
  phase-1 items were still not truthfully closed (recorded inline below), so
  items 4, 7 and 8 were re-opened to IN PROGRESS. Item 8's architectural decision
  is explicitly reversed: the custom in-Go OpenAPI-vs-TypeScript structural parser
  is superseded by a deterministic generated TypeScript SDK (ADR-6). No Git
  history was rewritten; the U+00A7 commit-history CI enforcement stays BLOCKED
  (section 8 item 9). The PR stays draft; PR-body finalization is phase 14.

Item 4 (bounded responses) re-opened counterexamples:
- `ServiceDetail` builds its relationships `Preview` from `Neighborhood.Edges`
  AFTER the neighborhood already truncated them, then forces `Truncated = true`.
  With more relationships than the neighborhood edge cap this reports
  `Total == Count` and `Truncated == true` at once — a `Total` that is only the
  count scanned before truncation, not the real relationship count. Every product
  Preview built from an already-bounded/paged result has the same class of bug.
- `Overview.Attention` and `Overview.RecentEvidence` are raw bounded arrays:
  Attention takes the first 10 of a paged result and discards the true total;
  RecentEvidence slices to 10. Neither carries total/count/truncated, so a
  consumer cannot tell 10-of-10 from 10-of-500.
- `RuntimePreview` bounds its OUTPUT but not the WORK: `sortedMapKeys(m)` allocates
  a slice proportional to `len(m)` before `maxRuntimeScan` can stop; at
  `maxRuntimeDepth` `capRuntimeValue(composite)` calls `fmt.Sprint` on the entire
  nested map/slice before truncating the string; and `RuntimePreview.Total` claims
  a true total even when the walk stopped early.
- `finding.Finding.EvidenceRefs` is an unbounded nested array inside the bounded
  findings previews. "Built-in producers emit small lists" is not a bound for a
  product API that accepts extension sources.

Item 7 (exact-content identity) re-opened counterexample:
- Product Impact requires a canonical OCI digest ref AND recorded-digest
  consistency, but `RevisionDetail`/`TargetDetail` compute
  `Immutable = IsDigestPinnedRef(ResolvedRef)` WITHOUT the consistency check. A
  revision whose `ResolvedRef` pins digest A while its recorded `Digest` is B
  appears immutable in detail yet is correctly rejected by Impact. The boolean
  `immutable` therefore does not mean the same thing everywhere. Separately,
  `ParseCanonicalOCIRef`'s repository check only proves some non-empty text exists
  before `@`, so a syntactically invalid repository is accepted despite the
  documented resolver-compatibility contract.

Item 8 (frontend wire contract) re-opened counterexample and reversal:
- The hand-maintained `productTypes.ts` duplicated the wire DTOs and the in-Go
  `producttypes_drift_test.go` existed only to prove that duplicate mirrored the
  OpenAPI. That is three sources of wire truth (Go, OpenAPI, TypeScript) kept in
  sync by a bespoke parser. The `SeverityUnknown` value emitted by the engine was
  silently absent from the hand-written TS severity union — exactly the drift a
  hand-written mirror invites. Superseded by ADR-6.

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
  that surface in OpenAPI, and `ProductEntityDetail` is a discriminated `oneOf`
  union in the contract. A small handwritten facade over the generated client adds
  ergonomics (named functions, `ApiError` translation, schema-version validation,
  union narrowing) but never redeclares a DTO field or builds an `/api/...` URL.

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
4. Make every product response genuinely bounded. IN PROGRESS (re-opened by the
   generated-SDK session; the branch-state block above records the fresh
   counterexamples: service-relationships / owner-attention double-truncation,
   raw Overview arrays, RuntimePreview computational unboundedness, and unbounded
   `finding.Finding.EvidenceRefs`). The earlier close is retained below for
   history; the new closure is recorded in section 17.
   The top-level slices were already capped, but several nested structures were
   still unbounded when this was first marked DONE:
   - `OwnershipInfo.Conflicts` is `[]string` and `serviceOwnership` appends one
     entry per conflicting revision owner (unbounded).
   - `RevisionDetailData` embeds `*readiness.Result` verbatim, whose `Checks
     []CheckResult` is unbounded (one per declared readiness claim).
   - `TargetDetailData.ObservedRuntime` is `map[string]any` copied verbatim from
     the source — recursively unbounded (nested size, not just top-level keys).
   - `ownerDetail` builds an attention preview from the ALREADY-paged
     `q.Attention(...)` result, so with more than `DefaultAttentionLimit`
     matching items the preview reports `Total = DefaultAttentionLimit` and
     `Truncated = false`, losing the true matched total (double-truncation). The
     same class applies to `serviceDetail`'s relationships preview, built from a
     `Neighborhood` capped at `DefaultMaxEdges` (120) below `MaxDetailPreview`
     (200).
   Resolution: `OwnershipInfo.Conflicts` is a `StringsPreview`; readiness is a
   product `ProductReadiness` with a bounded `ReadinessChecksPreview`; observed
   runtime is a depth-and-count-capped `RuntimePreview` (bounds nested size, not
   just top-level keys); and the owner-attention and service-relationship previews
   carry the true total and truncation of the paged/bounded result they wrap.
   `pkg/fleet/detail_bounds_test.go` proves each with an above-maximum / true-total
   adversarial case, and the boundedness audit below records a bound for every
   collection-bearing product field.
5. Correct entity-search semantics. DONE (revision-owner discoverability,
   structured owner matching, source-health filter, typed 422 on invalid combos).
6. Correct neighborhood semantics. DONE. Expansion affordances are now derived
   from the same requested knowledge views as the traversal, so an expected-only
   answer never advertises an expansion that exists only in observed knowledge
   (and vice versa). Regression tests cover expected/observed/differences across
   incoming and outgoing directions. True revision/deployment graph projections
   remain a later phase.
7. Correct contextual product impact (exact-content identity). IN PROGRESS
   (re-opened by the generated-SDK session: the `immutable` boolean does not mean
   the same thing everywhere — detail uses `IsDigestPinnedRef` while Impact also
   requires recorded-digest consistency; and `ParseCanonicalOCIRef` does not
   validate the repository with the real OCI grammar. See the branch-state block;
   the new closure is in section 17). The earlier close is retained below for
   history. Two concrete defects held when this was first marked DONE:
   - Identity contract broken on the normal dashboard OCI path. `dashboard
     oci://registry/repo` has its `oci://` stripped by `parseDashboardArgs`, so a
     bare `registry/repo` reaches `OCISource`. `pinRefToDigest` preserved the
     absence of the scheme and produced `registry/repo@sha256:...`. The transport
     `immutableRef` accepted that as immutable, but the REAL provider path
     (`Service.ImpactWithSnapshot` -> `resolveBundle` -> `graph.ParseDependencyRef`)
     treats a scheme-less ref as a LOCAL filesystem path, so a canonical Product
     Impact built from the normal dashboard OCI source passes the exact-content
     guard and then fails when the real provider resolves the "immutable" ref.
     Fix: an OCI-originated revision with a resolved digest MUST carry a canonical,
     immutable, resolver-compatible `oci://registry/repository@<validated digest>`
     `ResolvedRef`, regardless of the input spelling. The static-provider tests
     missed this because they never exercised the real resolve path.
   - Permissive immutable detection. `fleet.DigestFromRef`/`IsDigestPinnedRef`
     only checked that text surrounds one colon, so `@sha256:abc` counted as
     immutable. Replace with strict identity validation reusing
     `graph.ParseDependencyRef` (the same scheme parser the resolver uses) and the
     OCI `go-digest` primitive: require the `oci://` scheme, a named repository, a
     syntactically valid content digest whose algorithm/body validate, and digest
     equality with `ContractRevision.Digest`.
   Resolution: `pinRefToDigest` always emits the canonical
   `oci://<repository>@<digest>`; `fleet.ParseCanonicalOCIRef` is the SINGLE strict
   parser (reusing `graph.ParseDependencyRef` + the OCI `go-digest` primitive) that
   `immutableRef` and the detail `Immutable` flag both use; and
   `internal/cli/dashboard_impact_e2e_test.go` drives the complete real-provider
   vertical (OCISource -> fleet.Build -> Manager -> handler ->
   impactProviderForFleet -> Service.ImpactWithSnapshot -> BundleStore.Pull, no
   staticImpact) for the dashboard-stripped, tag and digest input spellings, plus
   the mutable / local / inconsistent rejections and the refresh-race 409.
8. Frontend/backend wire contract. IN PROGRESS, ARCHITECTURE REVERSED (see
   ADR-6). The hand-written `productTypes.ts` mirror and the in-Go structural
   drift parser `producttypes_drift_test.go` are superseded by a deterministic
   generated TypeScript SDK (`openapi-typescript` + `openapi-fetch`) with a CI
   drift gate; the whole dashboard frontend consumes the generated client through
   one transport seam shared by live HTTP and the WASM/static demo. The earlier
   in-Go-gate closure is retained below for history; the new closure is in
   section 17. Three concrete gaps held when this was first marked DONE:
   - Weak placeholders. `AttributedFinding.finding`, `RevisionDetail.readiness`,
     `RevisionDetail.validation` (`Preview<unknown>`) and `TargetDetail.findings`
     (`Preview<unknown>`) were `unknown`, not real DTO types; finite vocabularies
     (entity kind, completeness/source health, knowledge view, difference/link
     state, status) were plain `string`.
   - `ProductEntityDetail` was NOT a discriminated union: five optional payloads
     let zero or multiple coexist and the compiler could not narrow from
     `entity.kind`.
   - Request-operation drift. The backend `GET /api/fleet/entities` supports a
     `sourceHealth` filter that the typed `api.fleetEntities` never serialized,
     and the drift gate `TestProductTypesMatchOpenAPI` compared only property-NAME
     sets (so `total: number` -> `total: string` would pass) and ignored operation
     request parameters entirely, which is how `sourceHealth` drifted unnoticed.
   Resolution: `productTypes.ts` declares real DTOs (Finding, ProductReadiness,
   RuntimeFact) and literal-union vocabularies, and `ProductEntityDetail` is a
   discriminated union (kind-narrowed variants + `?: never` exclusivity + entity.kind
   type guards) with compile-time tests in `productTypes.typetest.ts` that
   svelte-check enforces. `api.fleetEntities` serializes `sourceHealth` and all six
   operations model every OpenAPI request parameter/body field. The rewritten
   `TestProductTypesMatchOpenAPI` is a FULL structural comparison (field types,
   arrays and item types, refs, required-vs-optional, bounded preview/page shapes,
   enum refinements) plus operation query/path/body parameters, and
   `TestDriftGateCatchesMutations` proves it catches number->string,
   required->optional, a changed array element type, a changed nested ref, a missing
   request parameter and a changed POST body field. (Deterministic generation from
   the OpenAPI was rejected: it cannot express the required discriminated union and
   would add a JS codegen toolchain + generated-artifact CI dance, whereas the
   in-Go structural gate is self-contained and its negative fixtures are pure unit
   tests.)
9. Complete U+00A7 enforcement. Gate capability DONE (the script scans authored
   files, committed generated docs, `--commits base..HEAD` messages and `--text`
   PR title/body, with fixtures per failure mode; the authored-file scan is
   blocking in CI). Commit-history + PR CI enforcement is BLOCKED on explicit
   history-rewrite authorization: 36 of the 98 `base..HEAD` commit messages carry
   a section sign, so wiring `--commits` into blocking CI would make the shared
   branch permanently red until those messages are rewritten (a destructive
   force-push the harness blocks without explicit user authorization). This is the
   one remaining, deliberately-deferred action.

### Product response boundedness audit (requirement, item 4)

Every collection-bearing field reachable from a product response was audited from
the exported OpenAPI (the 57-schema closure of the six product roots). Result:

- No `map`/`additionalProperties` field is reachable from any product response;
  the only maps in the whole schema set (`FleetSnapshot.{services,revisions,
  targets}`, `*Record.labels`, `TargetRecord.observedRuntime`) belong to the
  low-level `/api/fleet/snapshot` export and to record types that NO product
  response references (class C).
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
    (`ReadinessChecksPreview`), observed runtime (`RuntimePreview`, bounded on
    nested size), and the owner-attention / service-relationships previews (which
    now carry the true total and truncation of the paged/bounded result they wrap).
  - Class B (intrinsic small fixed maximum): `Neighborhood.views` and
    `EntityDetail.actions` and `Overview.entryPoints` (fixed vocabularies);
    `NeighborhoodNode.expansions` (at most the two directions).
- One residual, documented honestly: `finding.Finding.EvidenceRefs` is a nested
  array inside the (hard-capped) findings previews. It has no preview of its own.
  It is class B by construction: pacto's validation emits exactly one ref per
  finding, and the only multi-ref producer (the k8s source) is bounded by the
  Kubernetes object-size limit. A future product finding DTO would add an explicit
  bounded preview if untrusted custom `fleet.Source` implementations ever need
  defending against; today no code path or real source produces a large per-finding
  EvidenceRefs. This is the sole nested collection whose bound is external rather
  than an explicit truncated preview.

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
