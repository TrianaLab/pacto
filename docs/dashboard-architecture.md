# Dashboard architecture

The internal design of `pkg/dashboard` — the largest core package, and a
self-contained app component (HTTP server, multi-source aggregation, graph,
compliance, Kubernetes client, embedded single-page app) that the operator also
embeds. For deployment see [Dashboard container](dashboard-docker.md); for the
command's flags see [`pacto dashboard`](cli-reference.md#pacto-dashboard).

The sections below describe the **contract-exploration substrate**: the source
model, aggregation and the `/api/services`-family endpoints, which are what a
non-fleet host (the offline `pacto doc` export) serves. On a fleet-capable host
that substrate is presented through the product navigation — **Overview**,
**Services**, **Operational graph** and **Change analysis** — served by the
bounded `/api/fleet/*` product endpoints described in
[Operational graph](operational-graph.md).

## Source model

The dashboard exposes up to **four source types**:

| Source type | Role | Key type |
|-------------|------|----------|
| `local` | Contract from filesystem | `LocalSource` |
| `oci` | Contract from OCI registry | `OCISource` |
| `cache` | Contract baseline from the on-disk materialized cache | `CacheSource` |
| `k8s` | Runtime enrichment from Kubernetes | `K8sSource` |

`cache` is an **offline fallback**: it surfaces as a distinct source only when no live OCI registry is configured (see [Internal materialization](#internal-materialization) below).

## Discovery lifecycle

`OCISource` runs a **continuous background loop** (not one-shot):

1. **Shallow scan** (synchronous, at first `ListServices` call) -- one `ListTags` + `Pull` per configured repo. Fast.
2. **Deep discovery** (background goroutine) -- a breadth-first search across dependency refs, prefetching all semver versions. Closes `s.done` after the first cycle, ending the "discovering" UI state.
3. **Periodic rediscovery** -- after the first cycle, re-runs every 60 seconds (`ociRediscoverInterval`). Picks up new services, dependencies and versions pushed since the last scan. Each cycle rescans the internal cache and invalidates in-memory caches so enrichment data (hash, classification) surfaces immediately.

`K8sSource` is query-on-demand with a 3-second list cache TTL. Each API call fetches fresh data -- no background polling or watching.

## Source categories

Sources are divided into two categories with different roles:

**Contract sources** (`local`, `oci`, `cache`) provide the authoritative service definition -- interfaces, configuration, dependencies, version, owner. Exactly one contract snapshot wins per service. Priority: `local` > `oci` > `cache` (explicit dev intent wins over the registry baseline, which wins over the offline disk cache). `cache` only participates when no live `oci` source is configured.

**Runtime source** (`k8s`) enriches the contract with live cluster state -- contract status, conditions, endpoints, resources, observed runtime and readiness. Runtime data **never overrides contract content** (config and policy *content* always comes from the declared contract). The `enrichWithRuntime()` function in `source_resolver.go` enforces this boundary: it copies k8s-specific fields but preserves contract fields. The one computed exception is the `Validation` summary, which is derived (not declarable) and is recomputed from runtime state when k8s data is present — `computeSectionMeta` attributes that section to `k8s` in the section provenance.

## Resolution model

`ResolvedSource` (`source_resolver.go`) is the central aggregation layer. It combines contract and runtime sources into a unified view:

1. **Contract resolution** -- iterates contract sources in priority order (`local`, then `oci`, then `cache`). The first source that has the service wins. This produces one authoritative contract snapshot.
2. **Runtime enrichment** -- if k8s is available and has data for the service, runtime fields are layered on top of the contract snapshot without replacing any contract content.
3. **Service list** -- all sources are queried concurrently. Services are grouped by name across sources, merged using `mergeServiceEntry()`. The `Sources` array on each service lists all source types where that service was found.

`BuildResolvedSource()` constructs the `ResolvedSource` from the map of detected sources, automatically separating contract sources from the runtime source.

## Version history

Version history is merged across sources in a defined order (`resolverVersionSources` in `source_resolver.go` — `["k8s", "oci", "local", "cache"]`):

1. **k8s** -- PactoRevision CRDs are most authoritative (deployed versions with timestamps)
2. **oci** -- registry tags provide the full version catalog
3. **local** -- current on-disk version
4. **cache** -- the offline disk-cache baseline, consulted last

When a live OCI registry is configured, `OCISource.GetVersions()` already enriches its bare tag listings with hash, createdAt and classification from materialized bundles, so the separate `cache` entry contributes nothing extra. When no live registry is configured, the `cache` source supplies the version catalog from disk.

Versions are deduplicated by version string. When the same version appears in multiple sources, `enrichVersion()` fills empty fields (hash, createdAt, classification, ref) from later sources without overwriting existing values.

## Classification

`ClassifyVersions()` (`version_classify.go`) is a pure derivational function that computes diff classification between consecutive versions. It operates on `BundlePair` structs (tag + parsed bundle) and is independent of any data source.

Classification requires **materialized bundles** -- both the current and previous version must have their contract bundles available locally. If either bundle is missing (not yet fetched from the registry), that version pair is skipped and receives no classification. This is intentional: classification is computed on demand as bundles become available, not assumed.

## Internal materialization

`CacheSource` (`source_cache.go`) reads materialized OCI bundles from the disk cache (`~/.cache/pacto/oci/`). It plays **two distinct roles** depending on whether a live OCI registry is configured:

- **Internal enrichment (live OCI present).** It backs `OCISource`, providing contract hash, classification and createdAt enrichment for registry tag listings. In this mode it is invisible — `ActiveSources()` exposes the disk cache under the `"oci"` key.
- **Offline contract source (no live OCI).** It is promoted to a first-class `cache` source: `ActiveSources()` exposes it under the `"cache"` key and `BuildResolvedSource()` includes it as the lowest-priority contract source (`local` > `oci` > `cache`).

The flow when live OCI is present:

1. `OCISource.SetCache(cs)` wires a `CacheSource` internally
2. `OCISource.GetVersions()` lists tags from the registry (bare version + ref), then enriches each version with hash, createdAt and classification from the internal cache
3. Cache rescans happen in three places: after each background discovery cycle (`discoverAndPrefetch`), after resolve operations and after fetch-all-versions -- all call `RescanCache()` + memory cache invalidation so new data surfaces immediately

The `createdAt` timestamp from cached bundles reflects **local materialization time** (when the bundle was pulled to disk), not the registry push time. OCI registries do not expose push timestamps via tag listing.

## Section provenance (`SectionMeta`)

Every service-detail response carries a `SectionMeta` map so the UI can explain
*why* a section is absent and *where* present data came from — not just show a
blank. Each section reports a `state` and a `source`:

| State | Meaning |
|-------|---------|
| `present` | Has data from an available source. |
| `empty` | The section is applicable but was genuinely not declared. |
| `not_applicable` | Cannot apply to this contract (e.g. runtime sections on a reference-only contract). |
| `unavailable` | A source that would have supplied it was unreachable or absent. |

`SectionInfo` also carries `OverriddenBy` (the source that overrode a contract
value, e.g. `"k8s"` for the deployed `version`/`owner`) and a `Reason` note for
non-present states. `computeSectionMeta` (`sectionmeta.go`) derives the map from
what the resolver assembled; `markRuntimeOverrides` flags fields the k8s overlay
replaced. The top-level `RuntimeEvaluated` flag is true only when a Kubernetes
runtime overlay was actually applied, which lets the UI distinguish "no runtime
data yet" from "runtime evaluated, nothing to report". `SectionMeta` is populated
on **both** the resolved path and the single-source `getService` path, so every
response is fully explained regardless of which sources are active.

**Which source wins, per field.** This is the authoritative multi-source
provenance table — the dashboard and operator attribute fields identically:

| Field / section | Authority | Notes |
|-----------------|-----------|-------|
| interfaces, configurations, policies, dependencies, workload, state, capabilities, readiness, metadata | Declared contract (`local` > `oci` > `cache`) | Config & policy **content** always comes from the declared contract — even for reference-only contracts (the operator extracts schema content into status). |
| `version` | k8s overrides contract when deployed | `OverriddenBy: "k8s"`. |
| `owner` | k8s overrides contract when deployed | `OverriddenBy: "k8s"`. |
| namespace, `resolvedRef` | k8s only | Deployed-state fields; absent off-cluster. |
| contract status, conditions, endpoints, observed runtime, resources, ports | k8s only (runtime overlay) | `not_applicable` for reference-only contracts off-cluster. |
| `Validation` summary | Recomputed from runtime when k8s present | The one computed (non-declarable) field; `SectionMeta` attributes it to `k8s`. |

## `--no-cache` semantics

The `--no-cache` flag is a **cold-start mode**, not a fully stateless mode:

- At startup, `DetectSources()` skips `detectCache()` entirely -- no pre-existing cached bundles are scanned or indexed
- `CachedStore.DisableCache()` skips disk **reads** (no stale data), but disk **writes** remain enabled so same-session pulls are persisted
- The dashboard resolves `cacheDir` from `CachedStore.CacheDir()` when not explicitly set, ensuring `RefreshCacheSources()` knows where to find materialized bundles
- The `memCache` is always wired at startup (even with `--no-cache`) via `SetCacheSource(nil, memCache)`, ensuring `RefreshCacheSources()` can invalidate stale entries after on-the-fly creation
- If the user triggers "Fetch all versions" or lazy dependency resolution, `RefreshCacheSources()` creates a `CacheSource` on the fly from disk and wires it into the OCI source for enrichment
- The `onDiscover` callback is wired to `server.RefreshCacheSources` (not just `memCache.InvalidateAll`), so continuous background discovery also triggers on-the-fly `CacheSource` creation

## Graph model

The dashboard builds two graph representations:

**Global graph** (`buildGlobalGraph()`) -- a flat structure with `GraphNodeData` and `GraphEdgeData`, designed for D3.js force-directed visualization. Includes unresolved external dependencies as nodes with `status: "external"`. Edges are typed as `"dependency"` (contract deps) or `"reference"` (config/policy refs).

**Per-service graph** (`buildGraph()`) -- a recursive `DependencyGraph` with `GraphNode` and `GraphEdge`, used for tree visualization of a single service's dependency chain. Includes cycle detection.

Both graphs use **ref-alias mapping** (`buildRefAliases()`) to resolve OCI repository names (e.g., `my-service-pacto`) to contract service names (e.g., `my-service`), based on `imageRef` and `chartRef` fields from the service index.

`computeBlastRadius()` performs a breadth-first search on the reverse dependency graph (required deps only) to count how many services would be transitively affected if a given service breaks.

Multi-version **conflict detection** (`detectConflicts()` in `pkg/graph`) is a CLI-only concern used during `pacto graph` resolution; the dashboard does not call it, so version conflicts across the aggregated index are not surfaced through the dashboard API. A node can also appear with incomplete edges if its service details failed to load during a concurrent index rebuild; such nodes are rendered from the index alone.

The frontend renders the dependency graph through a single shared `GraphPanel` component (canvas + one toolbar + one legend), reused by the graph page, the service detail dependencies section and the owner detail view. The graph looks and behaves the same everywhere. The per-service view includes an SBOM section sourced from `ServiceDetails.SBOM`.

## Server and API

The HTTP server is built on [Huma v2](https://huma.rocks/) with typed I/O structs and automatic OpenAPI 3.1 spec generation. Static files (embedded SPA) and CORS are served on the raw `http.ServeMux`; only API operations go through Huma.

Key API operations:

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/health` | GET | Health status + version |
| `/metrics` | GET | Service and source counts |
| `/api/services` | GET | Service list with blast radius, compliance, checks |
| `/api/services/{name}` | GET | Full service details |
| `/api/services/{name}/versions` | GET | Version history |
| `/api/services/{name}/sources` | GET | Per-source breakdown |
| `/api/services/{name}/dependents` | GET | Reverse dependency lookup |
| `/api/services/{name}/refs` | GET | Config/policy cross-references |
| `/api/services/{name}/graph` | GET | Per-service dependency tree |
| `/api/graph` | GET | Global D3-ready dependency graph |
| `/api/diff` | GET | Classified diff between two versions |
| `/api/sources` | GET | Detected source status and discovery state |
| `/api/refresh` | POST | Force-refresh all sources |
| `/api/resolve` | POST | Lazy-resolve a remote dependency |
| `/api/versions` | POST | List registry tags, optionally fetch all |
| `/api/debug/*` | GET | Diagnostics (requires `--diagnostics` flag) |

When running alongside the Kubernetes operator, `EnrichFromK8s()` automatically discovers OCI repositories from CRD `resolvedRef` fields, enabling full contract bundles, version history and diffs without explicit OCI arguments.

## Version tracking

The dashboard computes version tracking semantics from two sources:

- **Version policy** (`versionPolicy`): the preferred source is the operator's `status.contract.resolutionPolicy` field (`Latest` → `"tracking"`, `PinnedTag` → `"pinned-tag"`, `PinnedDigest` → `"pinned-digest"`), normalized by `normalizeResolutionPolicy()`. When unavailable (non-K8s sources, older operators), `classifyVersionPolicy()` provides a conservative fallback that only classifies unambiguous cases (digest, explicit semver tag) and returns empty for ambiguous refs.
- **Latest available** (`latestAvailable`): the highest semver version from the existing version list. Computed by `computeLatestAvailable()`.
- **Update available** (`updateAvailable`): true when `latestAvailable` is a higher semver than the current `version`. Computed by `isUpdateAvailable()`. This is informational -- it does **not** affect contract compliance status.
- **Current version marker** (`isCurrent`): set on the `Version` entry matching `ServiceDetails.Version` via `markCurrentVersion()`.

Operator-provided `resolutionPolicy` is propagated through the K8s source (`serviceDetailsFromK8sStatus`), carried forward by `enrichWithRuntime()` and preserved by the index/detail enrichment in `server.go`, which applies the fallback only when no policy is already set.

These fields are populated during the service-index cache rebuild in `server.go` and surfaced through the existing `/api/services` and `/api/services/{name}` endpoints.

## Dashboard invariants

These rules must be preserved by future changes; the codebase-wide ones live in
[Architecture → Architectural invariants](architecture.md#architectural-invariants).

| Invariant | Rationale |
|-----------|-----------|
| K8s enriches runtime only, never overrides contract content | Contract is the source of truth for interfaces, config, dependencies, version. K8s provides live state (contract status, conditions, endpoints), and config/policy *content* always comes from the declared contract. The computed `Validation` summary is the one runtime-recomputed field, and `SectionMeta` attributes it to `k8s` so provenance stays honest. |
| Cache is a public source only as an offline fallback | When a live `oci` source is configured the disk cache stays internal to it (exposed under the `"oci"` key). Only when no live registry is configured is the cache promoted to a distinct `cache` source. A session shows `oci` **or** `cache` for the registry baseline, never both — so users are never confused about which is authoritative. |
| Contract source priority is `local` > `oci` > `cache` | Explicit dev intent beats the registry baseline, which beats the offline disk cache. `cache` only participates when `oci` is absent. |
| `resolverVersionSources` is `["k8s", "oci", "local", "cache"]` | Version history is merged in this order (see [Version history](#version-history)). |
| Classification requires materialized bundles | `ClassifyVersions()` diffs consecutive bundles. Without both bundles available, no classification is computed. |
| `--no-cache` skips startup scanning, not same-session materialization | Cold-start mode ensures deterministic initial state. `DisableCache()` skips disk reads but never disk writes, so bundles fetched during the session are persisted for enrichment (see [`--no-cache` semantics](#-no-cache-semantics)). |
| `SectionMeta` is populated on every service-detail path | Both the resolved (multi-source) path and the single-source `getService` path compute `SectionMeta`, so the UI can always distinguish `present` / `empty` / `not_applicable` / `unavailable` and label each section's `source`. |
| OCI discovery is continuous, not one-shot | New services and versions pushed after startup must surface without restarting the dashboard. The background loop re-runs discovery every 60 seconds. |
| K8s enrichment retries stop on permanent errors | If the Pacto CRD is not installed (`ListServices` returns "resource not found"), `EnrichFromK8s` nils the K8s source so the retry loop exits immediately instead of waiting 30 seconds. |
| UI data refresh must not disrupt user state | DOM morphing preserves scroll position, form values, `<details>` open/closed state and D3-managed containers. Debug panels use `patchDOM` instead of `innerHTML` replacement. |
| OpenAPI is the only wire truth for the dashboard | Huma generates the OpenAPI contract from the Go handlers; the TypeScript request/response types are generated from that contract into `pkg/dashboard/frontend/src/lib/generated/` and committed with a DO NOT EDIT notice. Handwritten frontend code may add ergonomics but must never redeclare a DTO field or build an `/api/...` URL by hand — a third, hand-maintained copy of the wire schema drifts silently. Enforced by `make check-dashboard-sdk-drift`, which regenerates them and fails on any diff. |

---

## See also

- [Architecture](architecture.md) — the repository layout these invariants sit inside
- [The Pacto model](model.md) — the roles the dashboard consumes
- [Observation sources](observation-sources.md) — configuring the sources the
  source model above assembles
- [Dashboard container](dashboard-docker.md) — running it outside Kubernetes
- [Testing architecture](maintainers/testing.md) — the gates that hold the
  invariants above
