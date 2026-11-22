---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

The dashboard shows the whole fleet on one map with its problems on it, from
40 services to 10000. The Overview leads with the map, the declared ↔ observed
match and contract completeness, with the attention list beside the map; the
Operational graph page is the same map on the whole page. The map zooms from
groups to services, names and details, groups by domain, owner team or
environment, switches between what is declared, observed or both and focuses
any service on its callers and callees. Needs attention leaves the navigation:
`#/fleet/attention` links open the Overview with the list filtered.

`pacto fleet map` and the MCP tool `pacto_fleet_map` give the same answers.
`pacto validate`, `pacto_check`, `pacto fleet get`, `pacto fleet status`,
`pacto fleet overview` and the matching fleet tools return contract
completeness: how much of each service its contract declares, in eight
sections, where unknown and inferred never count as declared.
