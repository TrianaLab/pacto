---
"@pacto/core": minor
---

Platform offerings report whether they serve. Each provider and each offering
carries `availability` per environment, derived from the provider's fresh
targets and its consumers' `DEPENDENCY_UNREACHABLE` reports, never from
compliance, and `unknown` when targets are hidden or a target source is
incomplete. An offering takes the best provider per environment, so a provider
the viewer cannot see keeps it from reading `unavailable`.

Offering list rows carry the providers' owners, the worst lifecycle,
availability, declared consumers and failed provisioning requests, and set
`targetsUnknown` instead of a 0 target count when targets cannot be counted.
The entity filters `lifecycle` and `failed` (`--lifecycle`, `--failed`, the
`lifecycle` and `failed` arguments of `pacto_fleet_entities` and query
parameters) and the sorts `failed` and `consumers` narrow and order the list,
and the aggregate tallies provisioning phases across offerings for the
dashboard's phase track.

The `lifecycle` attention category reports services that use a deprecated or
past-sunset provider or whose range excludes its successor
(`DEPENDENCY_SERVICE_DEPRECATED`, `DEPENDENCY_SERVICE_SUNSET`,
`SUCCESSOR_OUT_OF_RANGE`), with an Overview tile and
`pacto fleet status --lifecycle`. The provider lifecycle a consumer sees now
follows its range: it is the lifecycle of the newest provider revision the
range reaches, or the one `pacto.lock` pins, instead of the provider's newest
revision. A declared successor resolves to a revision in the fleet
(`SUCCESSOR_UNRESOLVED` when it does not), and each consumer use says whether
its range accepts the successor and the provider's newest supported version.
A provider's own lifecycle and successor, and an offering row's lifecycle and
the `lifecycle` filter, come from the provider's current revision: the newest
one a target runs, else the newest readable one, as on a service page. A
successor shipped under the same service no longer hides the deprecation of
the version still running.

The offering answer lists observed consumers, callers seen at runtime that
declare no use of the offering, apart from its declared consumers.
`pacto impact`, `pacto_impact` and `POST /api/fleet/impact` take an offering
subject (`offering:<name>`, `offering`, `offeringKey`) for `unavailable` and
`removed` events, and an `unavailable` event now lists each consumer's
resources. The offering page shows availability, observed consumers and the
successor, and links to **What if it becomes unavailable?**.
