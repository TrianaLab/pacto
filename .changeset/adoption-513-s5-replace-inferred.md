---
"@pacto/core": minor
---

When a person declares a service from its inferred twin with pacto declare,
the fleet shows one service: the inferred service is handed over to the
declared one through the declaration's inferredFrom, its observed calls
resolve to the declared service and replacements records link inferred-from
with the revision, the signer (by) and the workload the twin showed. The
hand-over needs a verified signature by the declaration's asker as a person
and an installation signer rule that allows them, so it never happens in a
--local fleet; a Pacto target that may run as the workload wins, two claims
of one workload merge nothing and an ignored link is reported as
INFERRED_LINK_IGNORED. Each hand-over raises an untimed INFERRED_REPLACED
attention item. pacto fleet get, pacto fleet explain, the TUI, the MCP tools
pacto_fleet_get, pacto_fleet_explain and pacto_fleet_entity and the dashboard
service page show fields, where each field of the current revision comes
from (declared, accepted, attested or inferred); explain adds a
REPLACES_INFERRED reason per replacement, and a restricted viewer gets
replacesHidden instead of a replacement whose workload they may not see.
replacement.at is now optional: an inferred-from replacement carries no time.
