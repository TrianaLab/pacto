---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

A failing health check is reported as `HEALTH_FAILING` (category
`Inconclusive`, severity `unknown`), and compliance stays `Unknown`: a failing
endpoint does not contradict the contract. The operator reports it when the
declared health endpoint answers with a 5xx other than 501, or when the
readiness probe on the declared health path keeps every pod out of Ready;
`pacto run` and `pacto agent` report it on a 5xx other than 501. A 501 or a
405 stays `EVIDENCE_INSUFFICIENT`, as do a workload scaled to zero and
endpoints the operator cannot map to the health port. `COLLECTION_FAILED` now
means only that a collector query errored, an EndpointSlice read included. The Evidence Server re-evaluates the same evidence
and reaches the same finding. The `CapabilityObserved` evidence payload gains
the optional `healthy` key, which only a present capability may carry. Strict
decoders reject unknown keys, so upgrade the Evidence Server with or before the
operator.

`pacto fleet`, `pacto mcp`, `pacto tui` and `pacto impact` now mark target
evidence older than 24 hours as stale by default, the window the dashboard
already uses. `--freshness 0` still skips the check, and every answer then
carries the `FRESHNESS_NOT_EVALUATED` limitation, so a stale count of zero is
never read as "nothing is stale"; the CLI also prints it as a note on stderr.
The limitation does not change completeness.

`pacto fleet overview`, the MCP tool `pacto_fleet_overview` and the dashboard
overview answer "how healthy is the organization and how much do we know" in
one shape. The overview summary gains `operationalHealth` (targets that are
healthy, failing their own health check, unknown or declare no health check),
`knowledge` with its gap kinds (`noSummary`, `noDomain`, `noInterfaceDocs`,
`noReadiness`, `notDeployed`, `unverifiedCapability`) and an `unknown` bucket,
`knowledgeItems`, `coverage` (assertions evaluated and required, and the
targets that report none) and `staleCompliantTargets`. Every compliance tally
gains `staleCompliant`, the Compliant targets or services that rest on stale
evidence: a subset, never subtracted from `compliant`, drawn hatched. The
entity list aggregate gains `targetHealth` and `knowledge`. The dashboard draws
an Operational health band and a Knowledge band, and a target that is unknown
is never counted as healthy. There is still no composite score.

Ownership splits a service whose contract Pacto cannot read from a service
that declares no owner: `ownership.unknown` and the filter value
`ownership=unknown` are new, labelled Owner unknown. Each business domain row
gains `failingHealth`, `ownerUnknown`, `notDeployed` and `withGaps`; owner and
domain pages gain operational health, knowledge, readiness, services that run
nowhere, services whose placement is unknown and unresolved and undeclared
dependencies; owner and domain list rows
gain `unknown`, `stale`, `failingHealth` and `withGaps` counts, and
`sort=health` ranks them by those counts. New filters and sorts: `gap`
(`--gap`), `sort=gaps` and `sourceHealth=incomplete`, which the new
Incomplete sources entry point opens. The attention categories `health` and
`knowledge` are new, and the dashboard's Attention view lists every category
with each item's reason.

Changed meanings: `ownership.unowned` and a domain row's `unowned` no longer
count services whose contract Pacto cannot read; the lead "Services needing
attention" tile and `servicesNeedingAttention` count every service with at
least one attention item, and the tile's `itemCount` carries the item total;
an `unknown` attention item names its cause in `code` (for example
`EVIDENCE_STALE` or `EVIDENCE_MISSING`) and keeps `UNKNOWN` only when no
cause is known; the CLI and MCP mark targets stale after 24 hours by default;
and the evidence payload gains `healthy`. The dashboard's sidebar badge counts
only attention items above `info`.
