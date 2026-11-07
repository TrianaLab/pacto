# Platform offerings reference

This page describes how [the operational graph](operational-graph.md) groups
services by platform offering: providers and their versions, consumers, the
consume snippet, provisioning status and what a restricted viewer sees. The
[operational graph reference](operational-graph-reference.md) covers sources
and queries.

## Providers

A service's platform offering and summary are the
[`service.offering`](contract-reference/sections.md#platform-offering) and
[`service.summary`](contract-reference/sections.md#summary) of its newest valid
revision, by the rule business domains use. When no revision declares an
offering, the service takes the one the operator read from the contract each
target runs (`status.contract.offering` and `status.contract.summary`). When
those operators report different offerings, the service has none and the graph
raises an `OFFERING_CONFLICT` limitation. Different summaries leave the summary
empty and raise no limitation.

Each distinct offering is an `offering` entity. The `offering` filter keeps the
services that provide it. Offerings are returned only to a query that asks for
the `offering` kind.

An offering's detail lists its providers and its consumers:

- each provider with its owner, summary, current revision, versions,
  interfaces, configuration and policies, at most 25 providers with the true
  total;
- each consumer that reaches a provider through a dependency, a configuration
  reference or a policy reference, with the range status of a dependency, the
  provider's lifecycle and the [provisioning status](#provisioning-status) of
  each environment.

Each provider says where its membership comes from in `offeringProvenance`:
`declared` when a contract revision declares the offering, `observed` when only
the operators observing its targets report it.

## Consumers

The `consumes` filter keeps the
services with a resolved dependency or configuration reference on a provider
of an offering; the dashboard's View all consumers link,
`pacto fleet entities --consumes` and the `consumes` argument of
`pacto_fleet_entities` use it.

A service's detail lists the offerings it consumes (`consumedOfferings`) and,
separately, inferred suggestions (`inferredOfferings`). A suggestion is an
external or unresolved dependency whose name exactly matches an offering or a
provider's name, ignoring case. Suggestions are never counted as consumption.

## Consume snippet

Each provider carries a consume snippet to add to a contract, built from its
newest readable revision, the one the offering page shows as current:

- a `dependencies` entry when the provider declares a workload or an interface,
  with `compatibility` set to the widest caret range that admits the newest
  version: `^<major>.0.0`, `^0.<minor>.0` for a 0.x version or the version
  itself for a prerelease. A version that is not semver gets an empty
  `compatibility` to fill in;
- a `configurations` entry per required configuration, with `ref` pinned to
  the current version, a `configuration` selector when the provider declares
  more than one and a `values` skeleton of the schema's required properties;
- a `policies` entry named after the provider when the provider declares a
  policy or ships `policy/schema.json`, and neither entry above applies.

The dependency `ref` is the provider's registry repository without a tag or
digest. A deprecated current revision adds a comment saying so. A provider
that is not published to a registry has no snippet.

## Provider versions

`pacto fleet versions <offering>`, `pacto_fleet_versions` and the offering page
list each provider's versions, newest first, with whether a target runs each
one and the service lifecycle it declares. With a range, such as `^16.0.0`,
each version carries `rangeStatus` (`satisfied`, `unsatisfied` or `unknown`)
and each provider names its `latestCompatible` version, checked the way a
dependency's range is checked. The list comes from the contract revisions the
snapshot holds, and its `completeness` is `fleet`: the registry may publish
other versions. An OCI-sourced fleet lists what the registry holds, and
[`pacto lock`](lockfile.md) resolves a range against the registry.

## Provisioning status

A provider adapter reports the state of a resource a consumer asked for as
[`ResourceProvisioned`](evidence.md#provisioning-reports) evidence: a signed
EvidenceSet through an Evidence Server, or an unsigned report in a v2
target-state file. Each report names the consumer revision by digest, the
configuration reference or dependency that asked and the environment. Pacto
joins reports onto the offering's uses at query time, one status per
environment, with the provider, external reference, producer, signing key,
source, report time and `verified`, true only for signed reports. Every
environment the consumer's current revision declares has a status. A use whose
consumer points more than one configuration or dependency at the provider has
one status per entry, named in `entry`.

A status reads a phase only from a current report. It reads `Unknown`, with a
reason and the phase the newest report gave in `reportedPhase`, when:

- no provider has reported for that use;
- the newest report is older than the freshness window, or 24 hours when
  `--freshness` is 0;
- the report's outcome is not `Observed`;
- the newest report is about another revision of the consumer, so a provider
  switch never inherits the old provider's `Ready`;
- two current reports, fresh and about the current consumer revision, from
  different producers or sources disagree, which also raises a
  `PROVISIONING_CONFLICT` limitation and lists their phases in `conflict`.

Reports are ordered by the time the Evidence Server accepted them, and reports
from a file by their `observedAt`. A report dated more than five minutes after
it was accepted, or after the snapshot time for a file, is rejected. Reports that name no
revision in the snapshot, or a use that revision does not resolve, are dropped
and counted in one `PROVISIONING_UNMATCHED` limitation, and a snapshot keeps at
most 10000 reports (`PROVISIONING_TRUNCATED`).

## Offerings under a restricted view

A viewer restricted to some namespaces sees a provider that has no Kubernetes
target, with its owner, offering and versions, when its contract declares the
offering and every Kubernetes source is available. All of that provider's
targets are removed from the view and its versions omit `inUse`. Any provider
with a target in a namespace the viewer cannot read omits `inUse` too. With a
Kubernetes source unavailable or partial, the absence of a target proves
nothing, so the provider stays hidden.

Consumers follow their own visibility. A consumer the viewer cannot see is
never named: the offering counts them in `hiddenConsumers`, and its
provisioning statuses, external references and namespace stay hidden. A
`PROVISIONING_CONFLICT` reflects only the reports the viewer can see, and
`PROVISIONING_TRUNCATED` counts only the dropped reports about visible
consumers. A visible
consumer whose configuration reference resolves to a hidden provider has the
reference validated again without it, so the reference reads
`CONFIG_REF_UNVERIFIED` and no finding carries what the hidden provider
declares.
