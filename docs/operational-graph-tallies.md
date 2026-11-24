# Operational graph tallies

This page lists the counts the [operational graph](operational-graph.md)
returns beside its rows: the overview summary, the entity list aggregate, the
owner and domain summaries and the attention categories.
[Organizational health and knowledge](operational-graph.md#organizational-health-and-knowledge)
explains what they mean. Every tally is a partition with its own unknown
bucket, and Pacto computes no score from them. Under a restricted view each
tally counts only what your view sees.

## Entity list aggregate

The dashboard's entity list and the TUI carry an aggregate over the complete
matched population, computed before paging:

| Tally | Partitions | Buckets |
|-------|-----------|---------|
| `serviceCompliance` | matched services | compliance states, rolled up from each service's targets, plus the `staleCompliant` subset |
| `targetCompliance` | matched operational targets | compliance states as observed per target, plus the `staleCompliant` subset |
| `targetHealth` | matched operational targets | `available` · `degraded` · `unavailable` · `unknown` · `notObserved` |
| `ownership` | matched services | `consistent` · `conflicting` · `unowned` · `unknown` |
| `readiness` | matched contract revisions | `passing` · `belowThreshold` · `expired` · `notDeclared` |
| `knowledge` | matched services that are no golden path | `described` · `withGaps` · `unknown`, and `gaps` per kind |

`notDeclared` is its own readiness bucket so that a missing assessment stays
separate from a failing one. `staleCompliant` counts, of `compliant`, how many
rest on stale evidence; it is a subset and is never subtracted from
`compliant`. `ownership.unowned` counts only services whose read contracts
declare no owner; a service whose contract Pacto cannot read counts in
`ownership.unknown`.

## Operational health and knowledge

| Field | Buckets | Meaning |
|-------|---------|---------|
| `operationalHealth` (overview), `health` (owner and domain) | `available` | The revision declares a health check, a source evaluated it on fresh evidence and no finding names it. The dashboard and CLI call it Healthy |
| | `unavailable` | The health check answered and reports it is failing (`HEALTH_FAILING`). Shown as Failing |
| | `unknown` | Pacto cannot tell: no evaluation, stale or no evidence, an unresolved or quarantined target or another finding on the health capability |
| | `notObserved` | The revision declares no health check |
| | `degraded` | No source reports degradation yet, so it reads 0 |
| `knowledge` | `described`, `withGaps`, `unknown` | Partitions `services`. A service with two gaps counts in two kinds and once in `withGaps` |
| `knowledge.gaps` | `noSummary`, `noDomain`, `noInterfaceDocs`, `noReadiness`, `notDeployed`, `unverifiedCapability` | Services per gap kind. `notDeployed` counts only when every source reporting targets is complete |
| `knowledgeItems` | | How many `knowledge` attention items the gaps and recent takeovers raise; the count on the overview tile |
| `match` | `services`, `dependencies`, each `matched` · `expectedOnly` · `observedOnly` · `insufficient`; `percent`, `coverage`, `completeness`; `resources` (see [resources through offerings](#resources-through-offerings)) | How far what the contracts declare is from what runs. `percent` is matched over what Pacto can observe and `coverage` is what Pacto can observe over everything counted; both are absent when their base is 0. An inferred service and a runtime call no contract declares are `observedOnly`; a stale or quarantined target is never proof a service runs |
| `crossEnvironmentCalls` | | How many `cross-environment` attention items there are; the count on the overview tile |
| `coverage` | `evaluated`, `required`, `reporting`, `notReporting` | Assertions summed over the targets that report coverage; `notReporting` counts targets whose source reported none |
| `staleCompliantTargets` | | Of `compliantTargets`, how many rest on stale evidence |

## Declared and observed match

The overview's `match`, each group of the [fleet map](fleet-map.md) and each
coverage record count services and dependencies in four states:

| State | A service | A dependency |
|-------|-----------|--------------|
| `matched` | Declared, with a visible target that is neither stale nor quarantined | Declared and observed |
| `expectedOnly` | Declared and running nowhere, counted only when Pacto sees every place it could run | Declared and never observed by a source that could see it |
| `observedOnly` | Inferred: running with no contract | Observed and declared by no contract, or with an inferred service on one end |
| `insufficient` | Anything else, such as a service whose only targets are stale | No observation can judge it |

`percent` is `matched` over `matched`, `expectedOnly` and `observedOnly`, and
`coverage` is that sum over every service and dependency counted. With nothing
observable neither is given, so the dashboard reads "Pacto observes nothing
yet". Golden paths, external systems and dependencies on services you cannot
see are left out, and a configuration reference has no verdict.

Grouped by environment, a node is a service running in that environment. It
is `matched` when its revision declares the environment or declares none, and
`observedOnly` when it declares other environments only. A revision that
declares an environment it runs nowhere in is `expectedOnly` only when Pacto
sees every place it could run, else `insufficient`. A dependency counts in an
environment when both ends run there; its observation stays per service.

## Resources through offerings

Each [managed resource](inferred-contracts.md#managed-resources) of an
external system is **through an offering** only when a verified, current
`ResourceProvisioned` record names it, by its object, its verified Crossplane
claim or its full identity, and comes from the provider of the offering its
consumer uses. An object or claim matches only a record whose environment
places the consumer on the resource's cluster. Every other resource is
**outside contract**, including one whose record is stale, unsigned or from
another provider, so an unknown never counts as through an offering. The
record's `provider` must be the offering provider's service name: an adapter
slug such as `aws` or `crossplane` leaves the resource outside contract.

When a source read any managed resource, the overview's `match` carries
`resources`: `throughOfferings` over `discovered`, with `percent`, which the
Overview reads as "Resources through offerings: 3 of 7". The
[fleet map](fleet-map.md) badges an external system that has resources
outside contract, and `pacto fleet entities --outside-contract` (MCP
`outside_contract`) lists those systems.

Two paths lead into a contract, both through `pacto claim --offering` or the
External page: join an offering that already has providers, or propose a new
one. The claim records the offering as intent in
`run.pacto.external.offering`, never as `service.offering`, and joining an
offering another team provides, or one whose providers were not all read,
writes a `draft-<n>` a member of that team accepts. The
resource stays outside contract until the offering's adapter reports it.

## Contract completeness

`pacto validate`, `pacto_check`, a service's detail and the fleet answers
carry `contractCompleteness`: how much of a service its contract declares, in
eight sections. A section reads `declared`, `inferred` (the contract does not
fully declare it and the inferred record holds a field for it), `missing`,
`unknown` or `not-applicable`, and a gap the inferred record can fill carries `declare`,
the YAML to add.

| Section | Applies when | Declared when |
|---------|--------------|---------------|
| `interfaces` | the contract declares an interface or Pacto infers one or a port | every interface names a document (`ref`) |
| `configuration` | the contract declares a configuration or Pacto infers configuration fields | every configuration has a schema or a reference (`ref`); values alone read `missing` |
| `dependencies` | the contract declares a dependency or Pacto infers one | every dependency accepts a version range |
| `owner` | always | the contract names an owner |
| `summary` | always | the contract declares `service.summary` |
| `domain` | always | the contract declares a business domain |
| `runtime` | the contract declares a workload or Pacto infers the workload or a health check (an image alone does not count) | it declares the workload and a health capability |
| `readiness` | the service is no golden path | the contract carries a readiness assessment with at least one claim |

`percentComplete` is declared sections over sections that apply; `inferred`
and `unknown` count only in the base, so neither is ever complete. An owner
that only an attestation or an inference names reads `inferred`. A section
the contract declares only in part (an interface with no `ref`, a workload
with no health capability) reads `missing`, even when Pacto infers more of it. The fleet
figure is the mean over the services Pacto can judge, with `judged`,
`unknown`, `fullyDescribed` (every section that applies is declared) and the
three sections most often `missing` as `mostMissing` (an `inferred` or
`unknown` section never counts there, so each figure matches the services its
link selects).

The knowledge gaps keep their filters and attention items and land in the
checklist:

| Gap or cause | Section |
|--------------|---------|
| `no-summary`, `SUMMARY_UNKNOWN` | `summary` missing, or unknown |
| `no-domain`, `DOMAIN_UNKNOWN` | `domain` missing, or unknown |
| `no-interface-docs`, `INTERFACE_DOCS_UNREAD` | `interfaces` missing, or unknown |
| `no-readiness` | `readiness` missing |
| `not-deployed`, `PLACEMENT_UNKNOWN` | a note on `runtime` |
| `unverified-capability` | a note on `runtime`: declared, not verified |
| `CONTRACT_UNREADABLE` | every section unknown |

## Owner and domain summaries

Each row of the overview's `byDomain` carries `services`, `targets`,
`nonCompliant`, `failingHealth`, `unknown`, `stale`, `unowned`,
`ownerUnknown`, `notDeployed` and `withGaps`. Domains are ranked by
non-compliant targets, then failing, unknown, unowned and services with gaps.

An owner or domain page's summary adds `health`, `knowledge`, `readiness`,
`notDeployed`, `placementUnknown` (services with no visible target whose
placement Pacto cannot see, never counted in `notDeployed`),
`unresolvedDependencies` (declared dependencies that resolve to no known
service) and `undeclaredDependencies` (observed calls no contract declares).

Owner and domain list rows carry `unknown`, `stale`, `failingHealth` and
`withGaps` service counts beside `nonCompliant`, over the services an owner
owns outright or every service of a domain. `sort=health` ranks them by
non-compliant, failing, unknown, stale and gap services; `sort=gaps` puts the
rows with the most services that leave knowledge out first.

## Filters

| Filter | Values | Lists |
|--------|--------|-------|
| `gap` (`--gap`) | `no-summary`, `no-domain`, `no-interface-docs`, `no-readiness`, `not-deployed`, `unverified-capability`, `unknown` | Services with that gap, or those whose knowledge Pacto cannot judge |
| `ownership` (`--ownership`) | adds `unknown` | Services with no owner claim whose contract Pacto cannot read |
| `sourceHealth` | adds `incomplete` | Sources that are partial, stale or unavailable; the overview's Incomplete sources entry point opens it |
| `sort` (`--sort`) | adds `gaps` | Owners and domains with the most services that leave knowledge out first |

## Attention categories

Attention items fall into these categories, worst first. The dashboard's
sidebar badge counts only items above `info`.

| Category | Code | Severity |
|----------|------|----------|
| `non-compliant` | `NON_COMPLIANT` | error |
| `non-compliant` | `CAPABILITY_ABSENT`, a trusted producer reports an extension capability absent | error |
| `invalid` | `INVALID_CONTRACT` | error |
| `cross-environment` | `CROSS_ENVIRONMENT_CALL`, a workload in one environment calls a workload in another; a warning when last seen outside the recent window | error |
| `cross-environment` | `CROSS_ENVIRONMENT_PREVIEW_CALL`, the callee's environment is a preview | warning |
| `health` | `HEALTH_FAILING`, a target whose own health check is failing | warning |
| `provisioning` | `PROVISIONING_FAILED`, a provider reports a resource Failed | error |
| `provisioning` | `PROVISIONING_STALE`, the last report about the current revision is stale | warning |
| `unknown` | the cause: `COLLECTION_FAILED`, `HEALTH_FAILING`, `EVIDENCE_STALE`, `OBSERVATION_UNSUPPORTED`, `EVIDENCE_INSUFFICIENT` or `EVIDENCE_MISSING`, else `UNKNOWN` | warning |
| `stale` | `STALE_EVIDENCE` | warning |
| `unknown`, `stale` | `EXTENSION_UNREPORTED`, no fresh producer report on an extension capability of a revision in use | warning |
| `unresolved` | `UNRESOLVED_DEPENDENCY` | warning |
| `out-of-range` | `DEPENDENCY_RANGE_UNSATISFIED` | warning |
| `lifecycle` | `DEPENDENCY_SERVICE_DEPRECATED`, a provider the consumer's range reaches is deprecated | warning |
| `lifecycle` | `DEPENDENCY_SERVICE_SUNSET`, that provider is past its sunset | error |
| `lifecycle` | `SUCCESSOR_OUT_OF_RANGE`, the consumer's range excludes the provider's successor | error |
| `undeclared` | `DEPENDENCY_UNDECLARED`, a runtime call no contract declares | warning |
| `undeclared` | `UNCONTRACTED_CALLER`, a caller with no contract | info |
| `golden-path` | `GOLDEN_PATH_DEPLOYED` | warning |
| `readiness` | `MISSING_READINESS` | info |
| `knowledge` | the gap: `NO_SUMMARY`, `NO_DOMAIN`, `NO_INTERFACE_DOCS`, `NOT_DEPLOYED` or `UNVERIFIED_CAPABILITY`; or the cause of an unknown service: `CONTRACT_UNREADABLE`, `INTERFACE_DOCS_UNREAD`, `SUMMARY_UNKNOWN`, `DOMAIN_UNKNOWN` or `PLACEMENT_UNKNOWN`; or `INFERRED_REPLACED`, an inferred service a contract took over: a Pacto target's takeover shows for 24 hours, a declaration's hand-over stays while the declaration holds; or `INFERRED_PROVIDER`, services check a range against an [inferred contract](inferred-contracts.md); or `EXTERNAL_CHANGED`, an external system's checked inferred contract moved its OpenAPI digest (warning) or clients | info |
| `unowned` | `UNOWNED_SERVICE` | info |
| `cluster` | `CLUSTER_ISSUER_DIFFERS`, member clusters read under a group mapping: their apiservers refuse the hub's sign-in issuer, or the hub exchanges no token | warning |
| `unowned` | `UNOWNED_EXTERNAL`, an external system with consumers that no declared contract claims, or whose claiming service has no verified owner | info |

A revision with no readiness assessment raises a `readiness` item, so
`noReadiness` raises no `knowledge` item. A service whose contract Pacto cannot
read raises one `knowledge` item and no `unowned` item. The Overview tile
"Undeclared runtime dependencies observed" opens both kinds of `undeclared`
item. `UNOWNED_EXTERNAL` items rank by how many services use the system, most
first, ahead of the services with no owner; a restricted view counts the
consumers it hides and never names them. Each one is routed to the
installation's default approvers for routing only, which grants no right. The
Overview tile "Services and external systems with no owner" counts both kinds
of `unowned` item.

`nextAction` names the operation that fixes an item, `{verb, tool, cli}`:
`cli` is the command a person runs, always set, and `tool` the MCP tool,
named only when the answering server registers it.

- `edit-contract` (`pacto_edit`, `pacto ci`) on `DEPENDENCY_UNDECLARED`,
  `UNOWNED_SERVICE` (when a contract could be read), `INVALID_CONTRACT`,
  `MISSING_READINESS`, `DEPENDENCY_RANGE_UNSATISFIED`, `GOLDEN_PATH_DEPLOYED`,
  `INFERRED_REPLACED`, an `UNCONTRACTED_CALLER` with no workload Pacto can
  name and a range that excludes a provider's successor.
- `declare` (`pacto_declare`, `pacto declare <workload> --namespace <ns>`) on
  an inferred service's `INFERRED_PROVIDER` and an `UNCONTRACTED_CALLER` whose
  workload Pacto can name. The dashboard links it to the inferred service's
  [Declare panel](dashboard-service-page.md#declare-an-inferred-service).
- `claim` (`pacto_declare`, `pacto claim <identity> --owner <team>`) on
  `UNOWNED_EXTERNAL` and an unclaimed external system's `INFERRED_PROVIDER`
  ([claim an external system](inferred-contracts.md#claim-an-external-system)).
  A claimed one's `INFERRED_PROVIDER` carries `edit-contract`, to point the
  ranges at the claimed contract, and one a service you cannot see claims
  carries none.
- `accept` and `approve-cluster` are reserved verbs.

Pacto infers it from the item's code, beside the `nextStep` prose. It grants
nothing, and an unknown, stale, unresolved or non-compliant item carries
none.

## Freshness

`pacto fleet`, `pacto mcp`, `pacto tui` and `pacto impact` mark target
evidence older than 24 hours as stale by default, like the dashboard.
`--freshness 0` skips the check, and every answer then carries the
`FRESHNESS_NOT_EVALUATED` limitation, so a stale count of zero never reads as
"nothing is stale". The limitation does not change `completeness`.
