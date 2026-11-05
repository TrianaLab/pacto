---
"@pacto/core": minor
---

Agents and the CLI can ask the fleet about owners, business domains, platform
offerings and what needs attention: the `pacto_fleet_entities`,
`pacto_fleet_entity` and `pacto_fleet_attention` MCP tools and the
`pacto fleet entities`, `pacto fleet entity` and `pacto fleet attention`
commands. A new `undeclared` attention category lists runtime calls no contract
declares (`DEPENDENCY_UNDECLARED`) and callers with no contract
(`UNCONTRACTED_CALLER`), and one Overview tile, "Undeclared runtime dependencies
observed", opens it. Runtime calls and caller coverage the Kubernetes operator
reports now join the fleet, and leave declared edges `insufficient`.
`pacto_fleet_graph` and `pacto fleet graph` reach observed-only callers and mark
each node's `provenance`. `pacto_fleet_status` and `pacto fleet status` report
services with no declared owner, `total` and `truncated`. `pacto_fleet_search`
takes `offset`. `pacto_impact` reports `sources`. `pacto mcp --fleet` rebuilds
its snapshot when a call arrives 30 seconds or more after the last build and
shares it with `pacto_impact`, and `--dashboard-url` links fleet answers to
their dashboard pages.
