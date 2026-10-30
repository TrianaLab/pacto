---
"@pacto/core": patch
---

Report each consumer at its shortest distance from the changed service. A
consumer that depended on it directly and also through another dependent that
sorted first was reported as `transitive confidence=inferred`; `pacto impact`
now reports it as `direct confidence=contractual`. `pacto fleet graph`, the
dashboard fleet graph API and the MCP `pacto_fleet_graph` tool walk the graph
breadth-first: nodes come in ascending depth, `--max-depth` counts the shortest
distance and a capped answer keeps the nearest nodes.
