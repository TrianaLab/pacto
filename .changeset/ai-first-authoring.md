---
"@pacto/core": patch
---

`pacto init` and `pacto_create` with `pactify` read more of a service into its
contract. The workload's environment, including the ConfigMaps it reads,
becomes a configuration schema with each value as its default. A URL in it
that names another service in the cluster becomes a dependency: with that
service's contract in `--fleet`, its bundle and a range from its version;
without it, a placeholder range that the contract comment and Unknown name. A
readiness, liveness or startup probe becomes a health check.

`pacto init --describe-only` (`describe_only` in `pacto_create`) writes a
contract that only describes the service: no publish section, no signing key
and no `.pactoignore`. Without it, `init` still writes the publish section and
now says how to leave it out.

`pacto init`, `pacto_create` and `pacto_edit` flag an owner written as
`team/billing`, and `pacto_check` suggests the `pacto_edit` call that writes
`billing`. `pacto_check` also explains how to declare a service of
your own whose contract is not published yet, so it shows as unresolved until it
is. The contract schema and the `DEPENDENCY_REF_REQUIRED` message say the same.

The MCP server instructions, `pacto_schema` and `pacto --help` now describe
Pacto as an operational contract system with optional delivery, and
`pacto_schema` returns how to describe an AI agent as a service.
