---
"@pacto/core": minor
---

Add the Pacto operational graph and fleet query layer.

A new framework-independent package (`pkg/fleet`) composes many contracts,
contract revisions and operational targets into an immutable, deterministic
`FleetSnapshot` and a pure, network-free `Query` over it. It keeps the three
identities distinct — logical service, contract revision and operational target
— references the existing engine types rather than re-modelling them, and makes
incompleteness explicit: every snapshot and query answer carries an as-of time,
a completeness and structured limitations, so an unavailable source is never a
silent empty result.

New consumers ship on top of it: a `pacto fleet` CLI command group
(search/get/graph/status/snapshot/explain), five read-only MCP fleet tools
(`pacto_fleet_*`), and dashboard `/api/fleet/*` endpoints, all backed by the same
read model. The architecture boundary test enforces that `pkg/fleet` stays free
of Kubernetes, MCP and dashboard dependencies.

Backwards compatible: no existing flags, APIs or JSON shapes change.
