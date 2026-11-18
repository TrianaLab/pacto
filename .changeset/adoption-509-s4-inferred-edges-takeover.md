---
"@pacto/core": minor
---

Runtime calls with an inferred service on one end join the graph as `inferred`
edges, kept in the snapshot's new `inferredRelationships` with the sources that
saw them; they never satisfy a dependency, feed impact or clear a gate. When a
Pacto target takes over a workload that ran with no contract first, the
snapshot records it in `replacements`, the service answer lists it in
`replaces` and an `INFERRED_REPLACED` attention item names it for 24 hours.
`pacto fleet get`, `pacto fleet graph`, their MCP tools and the dashboard
entity page answer an old inferred key with the service that replaced it and
`replacedFrom`, and impact adds `SUBJECT_REPLACED`. An ambiguous takeover
merges nothing and adds `INFERRED_REPLACEMENT_AMBIGUOUS`.
