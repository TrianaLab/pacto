The **default** server exposes the four authoring tools below. A bundle reference and four flags select a different server, each with its own tools. See [Server modes](#server-modes) after this table:

| Tool | Description |
|------|-------------|
| `pacto_create` | Create a new contract from intent-level inputs (name, description, interfaces, runtime semantics), or with `from` from a golden path (stdio only). Supports dry run. |
| `pacto_edit` | Edit an existing contract: add or remove interfaces, dependencies and configuration references, add environments and set their values, set the service lifecycle or the golden path template, change runtime, update metadata. Checks values against a referenced contract's schema before writing. Supports dry run. |
| `pacto_check` | Validate a contract and return errors, warnings and actionable improvement suggestions. |
| `pacto_schema` | Return the Pacto format explanation and full JSON Schema reference. |

These four authoring tools operate on local contract directories (they read and write `pacto.yaml` on disk) and accept no `oci://` refs.

### Server modes

The default server exposes the authoring tools above. A bundle reference, `--root`, `--fleet`, `--publish` and `--delivery` each select a different server. Passing more than one is an error:

| Invocation | Server |
|------------|--------|
| `pacto mcp` | Authoring tools. |
| `pacto mcp <bundle-ref>` | Authoring tools plus the bundle's OpenAPI operations as executable agent tools, alongside a `pacto_skill` tool for any bundled skills. |
| `pacto mcp --fleet` | Authoring tools plus read-only [operational-graph](operational-graph.md) query tools. |
| `pacto mcp --publish` | Authoring tools plus `pacto_publish_plan` and `pacto_publish`, which publish a version to your registry. Stdio only. |
| `pacto mcp --delivery` | Authoring tools plus the delivery tools over the cluster your kubeconfig points at: read delivery state and, for an agent, ask for or cancel a change. There is no approve tool. |
| `pacto mcp --root <ref> [--root <ref>]` | A read-only contract catalog discovered from the named roots. Catalog mode registers only catalog tools, and none of them writes to disk. |

`--root` is repeatable and takes a local bundle directory or an `oci://` reference. The roots and their dependency closure are resolved once, at startup, through the same reference parsing, credentials and cache the rest of the CLI uses; after that the session is frozen, so every answer reflects startup state even if a registry tag moves. A root that fails to resolve stays visible with a classified reason, and the catalog reports itself as partial. The catalog reads only the named roots and their closure, never refreshes and persists nothing.

### Fleet tool arguments

The arguments each fleet query tool takes. Arguments are optional unless marked:

| Tool | Arguments |
|------|-----------|
| `pacto_fleet_search` | `text`, `owner`, `business_domain`, `offering`, `status`, `compliance`, `workload`, `scope`, `source`, `ready`, `not_ready`, `has_capability`, `has_dependency`, `template` (`only` keeps golden paths, `exclude` leaves them out), `limit`, `offset` |
| `pacto_fleet_get` | `service` (a logical service) **or** `target` (an operational target by key or name) |
| `pacto_fleet_graph` | `service`, `revision` or `target` to root the traversal, then `direction`, `transitive` and `max_depth` (`0` = unlimited) |
| `pacto_fleet_status` | `needs_attention` for every category, or any of `invalid`, `non_compliant`, `unknown`, `stale`, `unresolved_deps`, `unsatisfied_ranges`, `missing_readiness`, `unowned`, `undeclared`, `golden_path_deployed`; plus `limit`. No category, including empty input, reports every category |
| `pacto_fleet_explain` | `subject` (**required**): a service name or a target key or name |
| `pacto_fleet_entities` | `text`, `kind` (`service`, `owner`, `domain` or `offering`; empty lists all four), `owner`, `owner_key`, `business_domain`, `offering`, `consumes` (services that depend on or reference a provider of this offering), `template` (`only` lists golden paths, `exclude` leaves them out), `created_from` (the services created from this golden path, by service key), `ownership`, `status`, `sort`, `limit`, `offset` |
| `pacto_fleet_entity` | `kind` and `key` (**both required**): a service, revision, target, owner, source, business domain or offering |
| `pacto_fleet_attention` | `category`, `severity`, `owner`, `owner_key`, `business_domain`, `service`, `limit`, `offset` |
| `pacto_fleet_neighborhood` | `service` (**required**): a service name or key, then `direction` (`dependencies`, `dependents` or `both`, the default), `depth`, `views` (comma-separated `expected`, `observed` and `differences`; empty means `expected,observed`) and `max_nodes` |
| `pacto_fleet_versions` | `offering` (**required**), then `provider` (a key, or a name only one domain uses) and `range` (a semver range; each version then carries `rangeStatus` and each provider its `latestCompatible` version) |
| `pacto_impact` | `old_ref` and `new_ref` (**both required**), plus `include_observed` and `traces` |

Argument names differ from flag names: the substring filter is `text`. Every tool above closes its schema, so an unknown argument is an error.

With `--fleet`, the server rebuilds its snapshot when a call arrives 30 seconds or more after the last rebuild attempt, and `pacto_impact` analyses over the same snapshot as the other tools. Add `--dashboard-url <url>` to make search, graph, status, entities, entity, attention, neighborhood and versions answers carry an `href` to each entity's page on that Pacto dashboard, and each contracted consumer in a `pacto_impact` answer an `href` to its service page.

See [MCP Integration](mcp-integration.md) for setup and the [MCP reference](mcp-reference.md) for serving a bundle's operations as tools and the catalog surface.
