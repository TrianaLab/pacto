# Platform offerings reference

This page covers how [the operational graph](operational-graph.md) groups
services by offering.
[Offering availability and lifecycle](offering-availability.md) covers
availability, deprecated providers, observed callers and impact, and the
[operational graph reference](operational-graph-reference.md)
covers sources and queries.

## Providers

A service's platform offering and summary are the
[`service.offering`](contract-reference/sections.md#platform-offering) and
[`service.summary`](contract-reference/sections.md#summary) of its newest valid
revision, by the rule business domains use. With no such revision the service
takes the offering the operator read from each target's contract
(`status.contract.offering` and `status.contract.summary`). When
those operators report different offerings, the service has none and the graph
raises an `OFFERING_CONFLICT` limitation. Different summaries leave the summary
empty with no limitation.

Each distinct offering is an `offering` entity. The `offering` filter keeps the
services that provide it. Only a query for the `offering` kind returns them.

An offering's detail lists its providers and its consumers:

- each provider with its owner, summary, current revision, versions,
  interfaces, configuration and policies, at most 25 providers with the true
  total;
- each consumer that reaches a provider through a dependency, a configuration
  reference or a policy reference, with the range status of a dependency, the
  provider's lifecycle and the [provisioning status](#provisioning-status) of
  each environment.

Each provider says where its membership comes from in `offeringProvenance`:

- `declared`: a contract revision declares the offering;
- `observed`: only the operators observing its targets report it.

The dashboard marks a provider owner that no revision Pacto read declares.

A provider that runs nowhere is found in its registry, like
[golden paths](golden-paths-reference.md#discovery).

## The Offerings list

Each offering row of `pacto fleet entities --kind offering`,
`pacto_fleet_entities` and the dashboard's Offerings list carries:

- `owner`: the providers' owner, "2 owners claim it" when they disagree or
  "declared by contacts only" for one owner that names no team or DRI;
- `lifecycle`: the worst lifecycle over the providers' current revisions,
  each the newest revision a target runs, else the newest readable one
  (`supported`, `deprecated` or `sunset`); `unknown` when a provider is
  observed only, the revision a target runs is unreadable or a provider is
  hidden from the viewer;
- `hiddenProviders`: under a restricted view, the providers the viewer may
  not see, which `owner`, `consumers` and `failedRequests` leave out;
- `availability` and `availabilityProvenance`: the offering's
  [availability](offering-availability.md#is-an-offering-available) roll-up and where its worst
  environment comes from;
- `consumers`: distinct declared consumers, and `hiddenConsumers` under a
  restricted view, where `consumers` and `failedRequests` are lower bounds;
- `failedRequests`: distinct consumers with a `Failed` provisioning status;
- `targets`: the providers' observed targets, or `targetsUnknown: true` with
  no count when a provider's targets are hidden or a target source is
  incomplete. Pacto prints "observed targets unknown", never 0.

The `lifecycle` filter (`deprecated` or `sunset`) and the `failed` filter
narrow the list; `--sort failed` (the most consumers with a failed request
first) and `--sort consumers` order it. The
aggregate tallies every matched offering's provisioning statuses by phase in
`offeringPhases` and `offeringPhaseConsumers`, and names the three offerings
with the most failures in `failedByOffering`, where `services` counts the
consumers whose request failed. The dashboard draws it as the phase track.
Its **Inferred offerings** checkbox switches the list to the
[inferred offerings](inferred-offerings.md).

![The Offerings list: 20 provisioning requests across 5 offerings drawn as a phase track, with one failed postgresql request and the lifecycle and failed-request filters](assets/screenshots/dashboard-offerings-list-light.png#only-light)
![The Offerings list: 20 provisioning requests across 5 offerings drawn as a phase track, with one failed postgresql request and the lifecycle and failed-request filters](assets/screenshots/dashboard-offerings-list-dark.png#only-dark)

## Consumers

The `consumes` filter keeps the
services with a resolved dependency or configuration reference on a provider
of an offering; the dashboard's View all consumers link,
`pacto fleet entities --consumes` and the `consumes` argument of
`pacto_fleet_entities` use it. With `--phase`, it keeps the consumers with a
provisioning status of that offering in that phase, and `--environment` narrows
those statuses to one environment, such as
`pacto fleet entities --kind service --consumes postgresql --phase failed --environment production`.
That answer carries a `PROVISIONING_ENVIRONMENT_SCOPED` limitation: consumers
whose current revision does not declare the environment have no provisioning
status there.

A service's detail lists the offerings it consumes (`consumedOfferings`) and,
separately, inferred suggestions (`inferredOfferings`). A suggestion is an
external or unresolved dependency whose name exactly matches an offering or a
provider's name, ignoring case. Suggestions are never counted as consumption.
A use of a provider that declares no offering joins `consumedOfferings`, with
`offering` empty, once a provider reports on it. The service's
`hiddenProviders` is a lower bound on the resources from providers you may not
see. See [Resources a service consumes](provision-infrastructure.md#resources-a-service-consumes).

A [golden path](golden-paths.md) never consumes: the offerings its contract
reaches are the requirements of the services created from it. Its detail lists
them as requirements with the same entries a consumer's `consumedOfferings`
carries, and the `consumes` filter and the offering's consumer list leave the
path out. See the [golden paths reference](golden-paths-reference.md).

### Unresolved uses

`unresolvedUses` counts declared dependency, configuration and policy
references that may name a provider but did not resolve, so the consumer list
leaves them out. The match is `inferred`. Each consumer counts once per
reference type and repository, hidden services included. Above 0 the answer
carries `OFFERING_USES_UNRESOLVED` and is `partial`.

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
  more than one and a `values` skeleton of the schema's required properties.
  Each property gets the first candidate the schema accepts: its default, its
  first enum value, the minimum of a number or the empty value of its type. A
  property no candidate satisfies is left out of `values`, written as a
  comment line such as `# storageGi: required integer, set a value` and named
  in `missingValues`;
- a `policies` entry named after the provider when the provider declares a
  policy or ships `policy/schema.json`, and neither entry above applies.

The snippet carries the same entries twice: as YAML in `yaml` and as
structured contract entries in `entries` (`dependency`, `configurations` and
`policy`), so an agent can add them without parsing YAML. The dependency `ref`
is the provider's registry repository without a tag or
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
[`ResourceProvisioned`](provider-reports.md#provisioning-reports) evidence: a signed
EvidenceSet through an Evidence Server, or an unsigned report in a v2
target-state file. Pacto joins reports onto the offering's uses at query
time, one status per environment, with the provider, external reference, producer, signing key,
source, report time and `verified`, true only for signed reports. Every
environment the consumer's current revision declares has a status. A use whose
consumer points more than one configuration or dependency at the provider has
one status per entry, named in `entry`.
Only a signed `Ready` is positive: badges and tallies show any other `Ready`
as "Ready (unsigned)" in the info tone, and `phases.readyUnsigned` counts them.

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

### Adapters

An offering's `adapters` lists up to 10 programs that report on its requests,
by slug, producer and signature, from current reports, else the newest.
Adapters are `observed`, from visible consumers' reports only.

| Field | Meaning |
|---|---|
| `provider` | The adapter slug the reports carry, free text the adapter picks. A report with no slug reads "unnamed adapter" |
| `producer`, `verified` | The producer that sent the reports and whether they were signed |
| `environments`, `phases` | The environments its reports cover and the phase of each request, as the badge shows it if its own current report agrees, else `Unknown` |
| `disagrees` | Requests where its report disagrees with another adapter's, so the status is `Unknown` |
| `lastReportedAt` | When its newest report was observed |
| `total`, `count`, `truncated` | Every adapter that reports, how many `items` lists and whether the list stops short of `total` |
| `items` | The adapters, signed first, then by slug (an empty slug last) and producer |
| `conflicting` | Requests whose adapters disagree; each of those adapters is listed |
| `unattributed` | Requests no adapter reported on |

## Offerings under a restricted view

A viewer restricted to some namespaces sees a provider that has no Kubernetes
target, with its owner, offering and versions, when its contract declares the
offering and every Kubernetes source is available. All of that provider's
targets are removed from the view and its versions omit `inUse`. Any provider
with a target in a namespace the viewer cannot read omits `inUse` too. With a
Kubernetes source unavailable or partial, the absence of a target proves
nothing, so the provider stays hidden.

A consumer the viewer cannot see is
never named: the offering counts them in `hiddenConsumers`, and its
provisioning statuses, external references and namespace stay hidden. A
`PROVISIONING_CONFLICT` reflects only the reports the viewer can see, and
`PROVISIONING_TRUNCATED` counts only the dropped reports about visible
consumers. A visible
consumer whose configuration reference resolves to a hidden provider has the
reference validated again without it, so the reference reads
`CONFIG_REF_UNVERIFIED` and no finding carries what the hidden provider
declares.

Availability, observed callers and offering impact count what a restricted
view hides; see
[Under a restricted view](offering-availability.md#under-a-restricted-view).
