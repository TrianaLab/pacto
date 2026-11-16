# Offering availability and lifecycle

This page describes the answers the [operational graph](operational-graph.md)
gives about a [platform offering](platform-offerings.md) as a whole: whether
it serves, whether its providers are deprecated and what replaces them, who
calls it without declaring it and what happens if it goes down.

## Is an offering available?

Each provider and each offering carries `availability`: a state per
environment in `byEnvironment`, a roll-up `state` and the `asOf` time it is
judged against. Pacto derives it from what it observes. It never reads the
compliance verdict and the contract declares nothing about it. Availability
answers whether the capability serves, a separate question from the
[provisioning status](platform-offerings.md#provisioning-status) of the
resources consumers asked for: a database server can serve while one consumer's database failed.

Per provider and environment, a target is fresh when it is neither stale nor
quarantined and its evidence is less than 24 hours older than `asOf`. A fresh
target serves when no interface it declares reports `INTERFACE_ABSENT` or an
inconclusive finding such as `EVIDENCE_MISSING`; it serves nothing when every
interface its contract declares reports `INTERFACE_ABSENT`:

| What Pacto sees | `state` | `provenance` |
|---|---|---|
| A provider target is hidden from the viewer or a target source is partial, stale or unavailable | `unknown` | `unknown` |
| Every fresh target serves and no consumer reports it unreachable | `available` | `observed` |
| Fresh targets, some reporting `INTERFACE_ABSENT` | `degraded` | `observed` |
| Fresh targets, every one serving none of its interfaces | `unavailable` | `observed` |
| Fresh serving targets and a consumer target that reports `DEPENDENCY_UNREACHABLE` for it | `degraded` | `inferred` |
| No fresh provider target, and every consumer target that declares it reports it unreachable | `unavailable` | `inferred` |
| No fresh provider target, and some of those consumer targets report it unreachable | `degraded` | `inferred` |
| Fresh targets whose interfaces were never seen answering | `unknown` | `unknown` |
| Targets with no fresh evidence | `unknown` | `unknown` |
| No target and no report | `unknown` | `unknown` |

Each environment gives its `reason`, the `reporting` and `targets` counts and
at most five consumer targets in `unreachable`. Pacto reads no replica count,
so `reporting` counts targets with fresh evidence, whatever their readiness.
A managed provider outside the observed clusters has no target, so one
consumer that cannot reach it among others that do reads `degraded`, never
`unavailable`.
When no target or report places a provider in any environment, one entry
with an empty `environment` keeps the reason, and the CLI and the dashboard
call it nowhere observed, or no target you may see when `targetsHidden` says
every target is hidden from the viewer.

### How states roll up

A provider's `state` is `available` when every environment is available and
`unavailable` when every one is. It is `degraded` when an environment is
degraded, or one is unavailable and another available, and `unknown`
otherwise, so an unknown environment never reads as trouble. An offering
takes the best provider per environment, in the order `available`,
`degraded`, `unknown` and `unavailable`: a provider that may serve keeps the
offering from reading `unavailable`. A provider seen in no environment, or
with targets hidden from the viewer, keeps every environment from reading
`unavailable` and marks it partial; its nowhere-observed entry is left out
once another provider places the offering.

The offering page shows availability first, per environment.

![The postgresql offering page: the What if it becomes unavailable? action, the availability, providers, consumers and other callers sections and the phase track of its 11 requests](assets/screenshots/dashboard-offering-postgresql-light.png#only-light)
![The postgresql offering page: the What if it becomes unavailable? action, the availability, providers, consumers and other callers sections and the phase track of its 11 requests](assets/screenshots/dashboard-offering-postgresql-dark.png#only-dark)

## Deprecated providers and successors

A consumer that reaches a deprecated or past-sunset provider through a
dependency or a configuration reference gets one item in the `lifecycle`
[attention category](operational-graph-tallies.md#attention-categories), and
the Overview tile Services using a deprecated provider opens them:

| Code | Severity | When |
|---|---|---|
| `DEPENDENCY_SERVICE_DEPRECATED` | warning | The provider revision the consumer reaches is deprecated. |
| `DEPENDENCY_SERVICE_SUNSET` | error | That revision's sunset date has passed. |
| `SUCCESSOR_OUT_OF_RANGE` | error | The consumer's range excludes the provider's resolved successor. |

The lifecycle a consumer sees belongs to the newest provider revision its
range reaches, or the one `pacto.lock` pins, so a successor released under the
same service never hides the deprecation of the version a consumer still
uses. `pacto fleet status --lifecycle` and the `lifecycle` argument of
`pacto_fleet_status` report the same items. A provider's own `lifecycle`,
`successor` and an offering row's `lifecycle` come from its current revision:
the newest one a target runs, else the newest readable one, as on a service
page. The demo's postgresql targets still run 16.1.0, so its row reads
deprecated after 17.0.0 ships.

### Successors

A provider whose current revision declares a `successor` carries it in
`successor`. The last path segment of the `oci://` reference names a service
in the provider's registry domain, and its tag or digest names one revision.
A resolved successor is `inferred`, as a resolved dependency is. A successor
that names no revision in the fleet is `unknown`, gives its `reason` and raises
a `SUCCESSOR_UNRESOLVED` limitation, and no range is graded against it. Each
consumer use carries `successorRange` against the resolved successor and
`supportedRange` against `latestSupported`, the provider's newest readable
revision that declares no lifecycle and is newer than every revision that
declares one. A successor that is another service says so in its `reason`, and
no range for the old provider is graded against it: the next step is to change
the dependency. In a restricted view an unresolved successor's reason says
"you may see". The dashboard shows the latest supported
verdict when the successor cannot be graded. With a range,
`pacto fleet versions` and the offering page's range check name the successor
of the newest revision the range reaches, so `^16.0.0` still asks about 16.x
once 17.0.0 ships.

## Who else calls it

`observedConsumers` lists the services seen calling a provider at runtime
that declare no use of any provider of the offering, with provenance
`observed`, `lastSeen` and `stale` once the call is more than 24 hours older
than `asOf` or carries no time, at most 200 with the true `total`. A caller
with no contract is listed by name with `uncontracted: true` and no time, so its last sighting is unknown. Observed
callers are never counted in `consumers`. The offering page lists them under
Also calling a provider.

## What if an offering goes down

`pacto impact --unavailable offering:<name>` and `--removed offering:<name>`
ask what happens to the consumers of every provider of the offering at once.
The offering page links to the same question with
**What if it becomes unavailable?**. See
[Ask about an offering](impact-what-if.md#ask-about-an-offering).

## Under a restricted view

Availability is `partial` in every environment where the provider has
dependents or consumers the viewer cannot see, and its reason says so. A
provider the viewer cannot see is counted in `hiddenProviders`, and in each
environment it turns an offering's `unavailable` into `unknown`. Observed
callers the viewer cannot see are counted in `hiddenObservedConsumers`.
