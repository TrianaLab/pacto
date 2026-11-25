---
"@pacto/core": minor
---

`pacto fleet offerings --consumable`, the `consumable` argument of `pacto_fleet_entities`, `GET /api/fleet/entities?consumable=true` and the "Only offerings you may consume" filter of the dashboard's Offerings view list the accepted offerings a caller may consume. Each provider carries its version, digest, publisher, the parameters of its closed configuration schema with their `x-pacto-tier` and bounds, its classes, its guarantees and a snippet that `pacto validate` accepts. Acceptance covers the listed digest; run `pacto lock` to pin it. Providers Pacto could not check make the answer partial, and every other exclusion is counted per reason. No contract field or `pactoVersion` changes.
