The **default** server exposes the four authoring tools below. A bundle reference and four flags select a different server, each with its own tools. See [Server modes](#server-modes) after this table:

| Tool | Description |
|------|-------------|
| `pacto_create` | Create a new contract from intent-level inputs (name, description, interfaces, runtime semantics). Supports dry run. |
| `pacto_edit` | Edit an existing contract: add or remove interfaces and dependencies, change runtime, update metadata. Supports dry run. |
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
| `pacto_fleet_search` | `text`, `owner`, `status`, `compliance`, `workload`, `scope`, `source`, `ready`, `not_ready`, `has_capability`, `has_dependency`, `limit` |
| `pacto_fleet_get` | `service` (a logical service) **or** `target` (an operational target by key or name) |
| `pacto_fleet_graph` | `service`, `revision` or `target` to root the traversal, then `direction`, `transitive` and `max_depth` (`0` = unlimited) |
| `pacto_fleet_status` | `needs_attention` for every category, or any of `invalid`, `non_compliant`, `unknown`, `stale`, `unresolved_deps`, `missing_readiness`; plus `limit` |
| `pacto_fleet_explain` | `subject` (**required**): a service name or a target key or name |
| `pacto_impact` | `old_ref` and `new_ref` (**both required**), plus `include_observed` and `traces` |

Argument names differ from flag names: the substring filter is `text`. Every tool above closes its schema, so an unknown argument is an error.

See [MCP Integration](mcp-integration.md) for setup and the [MCP reference](mcp-reference.md) for serving a bundle's operations as tools and the catalog surface.
