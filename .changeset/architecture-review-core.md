---
"@pacto/core": major
---

Fixes from a whole-repo architecture review, and the breaking cleanups of the
v4 major: camelCase JSON everywhere, the deprecated v3 API removed and a few
APIs reshaped. Every breaking change is listed under **Breaking changes** with
its migration.

**Behaviour changes**

- An `extension` capability no longer keeps a contract at Unknown forever.
  Pacto has no evaluator for one, so `validation.Evaluate` now lists it in
  `Coverage.Unverified` as `capabilities[ref=<ref>]` instead of counting it as a
  required assertion and emitting `EXTENSION_EVALUATOR_UNAVAILABLE`. A contract
  declaring an extension can reach Compliant; that verdict says nothing about the
  extension. The code is removed (see below).
- An evidence observation with outcome `Observed` must carry every
  verdict-bearing payload key for its kind (`present`, `type`, `reachable` or
  `durable`, plus `conformant` for a present configuration). A missing or `null`
  key used to decode to `false` and read as a confirmed violation; it is now
  rejected. The payloads Pacto's own producers emit always carry these keys.
- `PACTO_NO_ANIM` and `PACTO_NO_UPDATE_CHECK` share one parser: Go's boolean
  spellings, with any other non-empty value read as set. `PACTO_NO_ANIM=0` or
  `false` now leaves animations on, and `PACTO_NO_UPDATE_CHECK=true` now disables
  the update check. `1` works as before.

**Fixes**

- MCP `pacto_check` runs the same validation path as `pacto validate`,
  including `pacto.lock` verification, so a drifted lockfile no longer passes.
- MCP `pacto_edit add_config_properties` writes the properties into the first
  configuration's schema file, merging with what is there, instead of declaring
  a configuration and dropping them. Edited files are written through a
  uniquely named temporary file that keeps the target's permissions.
- `pacto agent --docker-label-filter key=value` (repeatable) narrows `--docker`
  to containers carrying every given label, so one agent on a shared daemon can
  keep to one Compose project. The Compose demo now does.
- Evidence ingestion keys a record's domain by the same rule the fleet sources
  use (`contract.OCIDomain`), and keeps the evidence validation detail in the
  server log. Sending evidence uses a client with a timeout and a bounded
  response read.
- Free text in `pacto doc` tables escapes `|` and folds line breaks.
- The CLI reference now states that `PACTO_INSECURE_REGISTRIES` applies to
  every registry access and that an unknown `--output-format` is rejected.

**API additions**

- `contract.OCIScheme`, `TrimOCIScheme`, `SplitOCIDigest`, `SplitOCITag`,
  `SplitOCIRef` and `OCIDomain`: one lenient set of reference helpers, now used
  everywhere Pacto split references by hand.
- `contract.DocsDir` and `contract.WalkDocs`, `lock.ReadFS` and
  `capability.ToolsForContract`: the bundle readers the fleet, the offline view
  and MCP share.
- `finding.Registry` returns every registry row; `fleet.Paginate` and
  `fleet.Page` page a sorted list; `fleet.AttentionCategoriesList` returns the
  attention order as a copy; `fleet.SourceKindKubernetes` names the Kubernetes
  source kind.

**Breaking changes**

- **The Go module path becomes `github.com/trianalab/pacto/v4`** (was
  `.../v3`). Update every import, and install the CLI with
  `go install github.com/trianalab/pacto/v4/cmd/pacto@latest`.
- **camelCase JSON.** `finding.Finding`, `finding.SubjectRef` and
  `finding.EvidenceRef` (`code`, `severity`, `category`, `subject.kind`,
  `contractPath`, `message`, `evidenceRefs[].source` …), `readiness.Result` and
  `readiness.CheckResult` (`score`, `minScore`, `checks[].id` …),
  `contract.ValidationError`/`ValidationWarning`, `validation.ValidationResult`
  and the `--output-format json` of `pacto init`, `validate`, `pack`, `pull` and
  `push` (`dir`, `path`, `valid`, `errors`, `name`, `version`, `ref`, `digest`)
  were PascalCase on the wire. That reaches `pacto agent` and `pacto run`
  output, MCP answers, the dashboard `/api/fleet/*` answers and the
  Evidence Server's records and target projection. Migration: read the
  camelCase keys. Evidence records already stored in a registry and answers from
  an older Evidence Server still decode, because the decoder matches keys
  without regard to case. The evidence envelope (`pacto.dev/evidence/v1`) is a
  versioned protocol and does not change.
- **`pkg/dashboard.Server` takes an options struct.** `NewServer(dashboard.Options{UI,
  Host, Port, Version, CORSOrigin, Logger, Fleet, Impact, Refresh})` and
  `Serve(ctx)` replace `NewServer(ui)`, the seven setters (`SetFleetProvider`,
  `SetImpactProvider`, `SetFleetRefresher`, `SetListenAddr`, `SetVersion`,
  `SetCORSOrigin`, `SetLogger`) and `Serve(ctx, port, host...)`. The OpenAPI
  server URL is derived from `Host` and `Port`.
- **Fleet HTTP filters are enums.** `status` on `/api/fleet/services`,
  `/api/fleet/entities` and `/api/fleet/attention` accepts only the seven
  compliance states, and `kinds` on `/api/fleet/entities` is a comma-separated
  list of `service`, `revision`, `target`, `owner` and `source` checked item by
  item. An unknown value or an empty item (a trailing comma) is answered with 422
  naming the parameter (`query.kinds[1]`). Migration: send only listed values and
  no empty items.
- **`/api/sources` reports Kubernetes as `kubernetes`,** the fleet source kind,
  instead of `k8s`. The dashboard's source pill follows.
- **Product impact moved to `pkg/impact`.** `impact.Product`,
  `impact.ProductConsumer`, `impact.ConsumersPage`, `impact.TallyBucket`,
  `impact.ProductChange`, `impact.ChangesPreview`, `impact.ProductRevisions`,
  `impact.ImmutableRef` and `impact.BuildProduct` hold the route-neutral logic;
  `dashboard.ProductImpact` embeds `impact.Product` and adds hrefs. Gone from
  `pkg/dashboard`: `ProductChange`, `ProductChangesPreview`, `ProductRefsPreview`
  (owners and active targets are `ProductRefPreview`), `ImpactTallyBucket` and
  the `MaxImpact*`, `DefaultImpactConsumers` and `MaxChangeValueRunes` constants
  (now `impact.MaxProduct*`, `impact.DefaultProductConsumers` and
  `impact.MaxChangeValueRunes`). The JSON field names of `POST /api/fleet/impact`
  do not change; three OpenAPI component names do (`Impact.ChangesPreview`,
  `Impact.ProductChange`, `Impact.TallyBucket`).
- **MCP `pacto_create` no longer infers `state` from the description.** Words
  such as postgres, db, redis, cache, persistent or "stores data" used to make the
  contract stateful; a runtime that disagrees is now a confirmed contradiction,
  so a guess would turn into a false violation. Migration: pass `stores_data`,
  `data_survives_restart` and `data_shared_across_instances` when the service
  keeps state. Without them the contract is stateless.
- **Removed deprecated API.**
  - `pkg/finding`: `CategoryInterfaceMismatch`; `CodeExtensionEvaluatorUnavailable`
    and its registry row (a stored finding with that code no longer has a
    category); the retired `CodeInvalidInterfaceType`, `CodeInterfaceRefRequired`,
    `CodeInvalidCapabilityType`, `CodeCapabilityRefRequired` and
    `CodeUnsupportedPolicyTarget` (all reported as `CodeSchemaViolation`);
    `DefaultSeverity` (use `SeverityFor(code, true)`).
  - `pkg/fleet`: `BuildOptions.AllowPartial` (use `DisallowPartial`),
    `AttentionCategories` (use `AttentionCategoriesList()`), `ParseTargetKey`,
    `IsDigestPinnedRef` (use `ParseCanonicalOCIRef` or `ClassifyContentIdentity`),
    `FleetSnapshot.ObservedDependents` and `ObservedDependencies` (use the query
    neighborhood), `MemorySource.WithCollectFunc`, `ProductSeverityUnknown` (use
    `ProductSeverity(finding.SeverityUnknown)`), `LimitationRevisionConflict` and
    `LimitationRevisionContentMutable` (nothing emitted them).
  - `pkg/contractview`: `LookupValidation`, `ValidationCatalogEntry`,
    `NormalizeContractStatus`, the `Condition`, `EndpointStatus`,
    `ResourcesInfo`, `PortsInfo`, `ChecksSummary`, `ComplianceCounts` and
    `SectionInfo` types, the `Section*` and `VersionPolicy*` constants and the
    `ServiceDetails` fields nothing populated (`Endpoints`, `Conditions`,
    `ChecksSummary`, `Resources`, `Ports`, `RuntimeEvaluated`, `SectionMeta`,
    `VersionPolicy`, `LatestAvailable`, `UpdateAvailable`). `ComputeCompliance`
    takes only the contract status, and `ComplianceInfo` carries only `status`.
  - `pkg/dashboard`: the forwarders `ServiceFromContract`,
    `ServiceDetailsFromBundle`, `GlobalGraphFromResult`, `NormalizeContractStatus`,
    `ComputeCompliance`, `LookupValidation` and `ApplyLock` (call
    `pkg/contractview`), the inert `ComputeDiff`, `DiffResultFromEngine`,
    `GraphFromResult` and `ComputeRuntimeDiff` (call `diff.Compare` and
    `contractview.GlobalGraphFromResult`) and the aliases of the removed
    contract-view types and constants.
  - `pkg/oci`: `WithNameOptions` (use `WithInsecureRegistries`); `LocalOnly`,
    `RemoteAllowed` and the `ResolveMode` type, so `Resolver.Resolve`,
    `ResolveConstrained` and `ResolvePinned` drop their mode argument;
    `CachedStore.PullCached` and `PullCachedPinned` (use `Pull` and
    `PullPinned`); `ReadCachedRef` (use `ReadCacheEntry`); `Resolver.ListVersions`
    and `FetchAllVersions` (use `BundleStore.ListTags` with `FilterSemverTags`);
    `ExportedUserHomeDirFn` and `SetUserHomeDirFn` (use `t.Setenv("HOME", dir)`).
  - `pkg/evidenceingest`: `Source` and `NewSource` (read the evidence-source HTTP
    projection at `TargetsPath`), `MemoryStore`, `NewMemoryStore` and the
    `onAccept` argument of `NewHandler` (wrap `Store.Commit` to observe accepts).
  - `graph.RenderTree` (use `RenderTreeColored(r, graph.TreeColors{})`),
    `sbom.HasSBOM` (check `ParseFromFS` for a nil document),
    `otelobserver.Observe` (use `ParseTraces`, `DependencyEdges` and
    `EvidenceSets`), `semver.LessDesc` (use `Compare`) and
    `catalog.ContentID.Zero` (compare with `catalog.ContentID{}`).

**API additions in v4**

- `runtimefacts.CollectorKubernetes`, `CollectorKubernetesKernel` and
  `CollectorKernel` name the Kubernetes collectors; the operator uses them and
  `go run ./cmd/gendocs registry` publishes them.
