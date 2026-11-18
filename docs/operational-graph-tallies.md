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
| `coverage` | `evaluated`, `required`, `reporting`, `notReporting` | Assertions summed over the targets that report coverage; `notReporting` counts targets whose source reported none |
| `staleCompliantTargets` | | Of `compliantTargets`, how many rest on stale evidence |

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
| `invalid` | `INVALID_CONTRACT` | error |
| `health` | `HEALTH_FAILING`, a target whose own health check is failing | warning |
| `provisioning` | `PROVISIONING_FAILED`, a provider reports a resource Failed | error |
| `provisioning` | `PROVISIONING_STALE`, the last report about the current revision is stale | warning |
| `unknown` | the cause: `COLLECTION_FAILED`, `HEALTH_FAILING`, `EVIDENCE_STALE`, `OBSERVATION_UNSUPPORTED`, `EVIDENCE_INSUFFICIENT` or `EVIDENCE_MISSING`, else `UNKNOWN` | warning |
| `stale` | `STALE_EVIDENCE` | warning |
| `unresolved` | `UNRESOLVED_DEPENDENCY` | warning |
| `out-of-range` | `DEPENDENCY_RANGE_UNSATISFIED` | warning |
| `lifecycle` | `DEPENDENCY_SERVICE_DEPRECATED`, a provider the consumer's range reaches is deprecated | warning |
| `lifecycle` | `DEPENDENCY_SERVICE_SUNSET`, that provider is past its sunset | error |
| `lifecycle` | `SUCCESSOR_OUT_OF_RANGE`, the consumer's range excludes the provider's successor | error |
| `undeclared` | `DEPENDENCY_UNDECLARED`, a runtime call no contract declares | warning |
| `undeclared` | `UNCONTRACTED_CALLER`, a caller with no contract | info |
| `golden-path` | `GOLDEN_PATH_DEPLOYED` | warning |
| `readiness` | `MISSING_READINESS` | info |
| `knowledge` | the gap: `NO_SUMMARY`, `NO_DOMAIN`, `NO_INTERFACE_DOCS`, `NOT_DEPLOYED` or `UNVERIFIED_CAPABILITY`; or the cause of an unknown service: `CONTRACT_UNREADABLE`, `INTERFACE_DOCS_UNREAD`, `SUMMARY_UNKNOWN`, `DOMAIN_UNKNOWN` or `PLACEMENT_UNKNOWN`; or `INFERRED_REPLACED`, an inferred service a contract took over in the last 24 hours | info |
| `unowned` | `UNOWNED_SERVICE` | info |

A revision with no readiness assessment raises a `readiness` item, so
`noReadiness` raises no `knowledge` item. A service whose contract Pacto cannot
read raises one `knowledge` item and no `unowned` item. The Overview tile
"Undeclared runtime dependencies observed" opens both kinds of `undeclared`
item.

`nextAction` names the operation that fixes an item and the tool that makes
it, `{"verb": "edit-contract", "tool": "pacto_edit"}`, on
`DEPENDENCY_UNDECLARED`, `UNOWNED_SERVICE` (when a contract could be read),
`INVALID_CONTRACT`, `MISSING_READINESS`, `DEPENDENCY_RANGE_UNSATISFIED`,
`GOLDEN_PATH_DEPLOYED` and a range that excludes a provider's successor.
Pacto infers it from the item's code, beside the `nextStep` prose. It grants
nothing, and an unknown, stale, unresolved or non-compliant item carries
none.

## Freshness

`pacto fleet`, `pacto mcp`, `pacto tui` and `pacto impact` mark target
evidence older than 24 hours as stale by default, like the dashboard.
`--freshness 0` skips the check, and every answer then carries the
`FRESHNESS_NOT_EVALUATED` limitation, so a stale count of zero never reads as
"nothing is stale". The limitation does not change `completeness`.
