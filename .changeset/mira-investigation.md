---
"@pacto/core": minor
---

`--mira-source` reads a Mira node's service map into the fleet as observed
calls between services (source kind `mira`) on `pacto fleet`, `pacto mcp`,
`pacto tui`, `pacto impact` and `pacto dashboard`
(`PACTO_DASHBOARD_MIRA_SOURCES`). A declared dependency any source witnessed now
reconciles `matched`. Restricted views hide and count every caller that has no
namespace and every unresolved observed name, whichever source saw them. The
`pacto_fleet_neighborhood` MCP tool and the `pacto fleet neighborhood` command
list who a service depends on and who depends on it, with their owners, and
`pacto_impact` consumers link to their dashboard pages. Delivery history takes
`since`, `until`, `service` and `domain` and reports each change's `applied`
time, `cause` and `correlationID` and the Stage's service and environment. The
dashboard opens a service in Mira and filters delivery history by time. The new
"Investigate a problem with Mira" guide maps each incident question to a Pacto
or Mira tool.
