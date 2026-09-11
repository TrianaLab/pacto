---
"@pacto/core": minor
---

Retire the dashboard's second ingestion stack, and make the static export and the
published API document tell the truth about what they answer.

The dashboard used to run two independent ways of turning references into services
in one process: the original `DataSource` stack (`source_local.go`,
`source_oci.go`, `source_cache.go`, `source_k8s.go`, `detect.go`, its own cache and
its own multi-source resolver) and the operational graph in `pkg/fleet`, reached
through `SetFleetProvider`. They disagreed about freshness, about partiality and
about what "this source is available" means, and only one of them carried the
completeness envelope every fleet answer is supposed to carry. The `DataSource`
stack is gone. Every source the dashboard serves — local, OCI, cached, Kubernetes
and observation — now arrives through `pkg/fleet`, so `/api/sources` reports the
same health the fleet reports and an incomplete read is never rendered as an empty
one.

The contract-view types the static export and `pacto doc` render moved out to
**`pkg/contractview`**, a leaf with no HTTP and no Huma in it. `pkg/dashboard`
keeps every released name as an alias, so existing imports compile unchanged.

**Breaking, and deliberate: the whole `pkg/dashboard` multi-source surface is
removed.** Everything else in this release is additive, so this ships as a minor
with the break called out here rather than held for v4. A shim is not possible
for any of it: these names have no successor inside `pkg/dashboard` to forward
to. What replaced them lives in `internal/fleetsrc`, behind `pkg/fleet`, and is
reached through `SetFleetProvider`. A wrapper that accepted a `DataSource` and
ignored it would turn a compile error into a dashboard that silently serves
nothing, which is the worse failure.

```go
// before
srv := dashboard.NewServer(src, dashboard.EmbeddedUI())

// after
srv := dashboard.NewServer(dashboard.EmbeddedUI())
srv.SetFleetProvider(func(ctx context.Context) (*fleet.Query, error) { ... })
```

The full list, so nobody discovers it at compile time:

- Constructors: `NewServer` loses its `DataSource` parameter, `NewResolvedServer`
  is removed.
- `Server` methods: `SetResolver`, `SetCacheDir`, `SetCacheSource`,
  `SetOCISource`, `SetK8sRedetect`, `SetLazyEnrich`, `RefreshCacheSources`,
  `UpdateSourceInfo`, `WaitForVersionEnrich`. Source wiring, cache wiring and the
  enrichment handshake are all the fleet's job now.
- The source interface and its implementations, with their methods:
  `DataSource`, `LocalSource`, `OCISource`, `K8sSource`, `K8sClient`,
  `CRDDiscovery`, `CacheSource`, `ResolvedSource`, and the constructors
  `NewLocalSource`,
  `NewOCISource`, `NewK8sSource`, `NewCacheSource`, `NewResolvedSource`,
  `BuildResolvedSource`, plus `ContractRefProviderFromSource` and
  `RepoProviderFromSource`.
- The source-local cache: `Cache`, `CachedDataSource`, `NewMemoryCache`,
  `NewCachedDataSource`. The fleet snapshot is the cache now.
- Detection: `DetectSources`, `RedetectK8s`, `CurrentKubeContext`,
  `DetectOptions`, `DetectResult`, and the diagnostics types
  `SourceDiagnostics`, `LocalDiagnostics`, `OCIDiagnostics`, `K8sDiagnostics`,
  `CacheDiagnostics`.
- `ClassifyVersions` and `BundlePair`, which classified versions for a stack
  that no longer produces them.

**`pacto dashboard --diagnostics` is removed**, with the `PACTO_DASHBOARD_DIAGNOSTICS`
environment variable and the `DashboardConfig.Diagnostics` field behind it. The flag
existed to register `/api/debug/sources` and `/api/debug/services`, which reported on
the source stack; both endpoints went with it. The field is the one removal here from
a type that survives, and it is not kept as an inert bool on purpose: `DashboardConfig`
is published as a JSON Schema, so a retained field would advertise a diagnostics panel
that no longer exists. `/api/sources` now carries the fleet's own health and
completeness, which is what the panel was reading for. Scripts passing the flag will
fail with an unknown-flag error rather than silently changing behaviour.

`CRDDiscovery` in the list above is a re-export of an `internal/k8sclient` type, and
it goes for a second reason beyond its stack: `pkg/dashboard` is now gated k8s-free by
`tests/architecture/boundary_test.go`, so keeping the alias would pull client-go back
into a package that must stay consumable without it.

If you were importing any of these, you were driving the dashboard's private
ingestion. Build a `fleet.Query` and hand it to `SetFleetProvider` instead; that
is the same data with a completeness envelope attached.

Everything else that moved kept its name. `ApplyLock`, `AggregatedService`,
`SourceInfo`, `ServiceNameInput`, `ComputeDiff`, `DiffResultFromEngine`,
`GraphFromResult` and `ComputeRuntimeDiff` are all still exported from
`pkg/dashboard` with their v3 signatures, the last four marked deprecated
because nothing in Pacto calls them any more.

`ServiceDetails.SectionMeta` and the `Section*` vocabulary around it are
deprecated. Their two writers went with the ingestion stack and the field has had
no producer since; it is `omitempty`, so it is simply absent on the wire. The names
stay through v3 and are removed at v4.

**`get-service-graph` is removed from the published OpenAPI document.** Nothing
answered it: the live host serves `/api/fleet/services/{name}/graph` and the
offline export serves the global `/api/graph`. The generated TypeScript client's
`serviceGraph` facade had no callers and goes with the operation. Three operations
that the document listed and the export could not answer — a specific version, the
per-source breakdown and a same-version diff — now have real fixtures instead, so
the offline single-service app no longer meets a 501 on a call it makes itself.

**Behaviour change in `pacto doc`.** Every service page now renders all eleven
domain sections; an empty one says "None declared" instead of vanishing. The
contents rail always listed all eleven, and the presence map it consulted had no
writer, so it was offering jump targets that scrolled nowhere.

Also in `pacto doc`: a dependency's display name is derived by parsing the
reference rather than by scanning it for the last colon. A reference carrying a
registry port rendered as `localhost`, and one with no tag at all rendered as
`oci` — both now render the repository's last path component, like every other
reference already did.
