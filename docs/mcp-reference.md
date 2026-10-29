# MCP reference

This page lists the tools, inputs and limits of `pacto mcp`. For a
walkthrough, start with [Drive Pacto from an agent](mcp-integration.md).

## Three tool families and their boundaries

| Family | Tools | What they do | Safety boundary |
|--------|-------|--------------|-----------------|
| **Authoring** | `pacto_create`, `pacto_edit`, `pacto_check`, `pacto_schema` | Create, edit and validate Pacto *contracts*. | Operate on contract files only. `pacto_edit` writes only after validation. |
| **Generated service** | Derived per operation from a bundle's OpenAPI interfaces (`getUser`, `createRefund`, …) | Invoke the *live service* the contract describes. | Read-only (`GET`/`HEAD`) unless you pass `--allow-writes`; every call has a 30-second timeout and follows no redirect, so a 3xx comes back to the agent as the result. |
| **Fleet query** | `pacto_fleet_search`, `pacto_fleet_get`, `pacto_fleet_graph`, `pacto_fleet_status`, `pacto_fleet_explain`, [`pacto_impact`](impact.md#the-mcp-tool-and-the-dashboard) | Read-only queries over the *operational system*: services, revisions, targets, relationships and status. `pacto_impact` projects a contract diff onto that system to report which consumers a change affects. | Always read-only: they write nothing. They still make network calls, and the server that hosts them may also register write tools; see [Fleet query safety](#fleet-query-safety). Catalog mode is the only server with no write tool. |

Outside these families: `pacto_skill` (a bundle's skill files) and `pacto_catalog_revision` (one revision from the catalog, see [Catalog mode](#catalog-mode)). The publish and delivery tools are listed [below](#publish-and-delivery-servers).

These tasks are CLI-only: inspecting a registry contract, resolving a dependency
graph, diffing revisions, generating docs and the two non-query `pacto fleet`
operations, `reconcile` (declared dependencies against observed traffic) and
`snapshot` (the whole read model as one document). Only `pacto_publish` pushes,
and no tool deploys. In delivery mode an agent can ask for a change, and Pacto's
checks and people decide it. [Server modes](cli-reference.md#server-modes) lists which tools each
invocation registers.

!!! warning "Tools carry no safety annotations"
    Pacto ships **no MCP tool annotations**. A `tools/list` response carries no
    [`annotations`](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#tool-annotations)
    member (`readOnlyHint`, `destructiveHint` or `idempotentHint`) on any tool,
    including `pacto_create`, `pacto_edit` and a generated
    tool such as `createRefund`, which moves money. Build allow-lists by hand
    from the tool name. `pacto_check`, `pacto_schema`, `pacto_skill`, `pacto_fleet_*`,
    `pacto_impact` and `pacto_catalog_revision` are read-only; `pacto_create` and
    `pacto_edit` write contract files. Generated service tools carry no `pacto_`
    prefix and reach a live service. They are read-only only when the server
    was started without `--allow-writes`; otherwise treat every unprefixed tool
    as able to change the live service. A bundle cannot
    claim a `pacto_` name.

### Fleet query safety

- **They are read-only.** They project the [Pacto Operational Graph](operational-graph.md) and write nothing. The `pacto_fleet_*` tools answer from the snapshot built at startup. `pacto_impact` re-resolves both refs and rebuilds the snapshot on every call, so each invocation reaches your registry, and your cluster if `--k8s` is on, with the server's credentials.
- **They expose knowledge only.** They never grant, scope or revoke a permission.
- **Partial or stale results are incomplete knowledge.** Every answer carries an `asOf` time, a `completeness` value and structured `limitations`. Branch on those before trusting an answer.
- **Under partial coverage, a missing result means unknown.** If a source was unavailable, "not found" means "not known here".
- **The snapshot is frozen for the session.** A service deployed after the server started appears in `pacto_fleet_*` answers only after you restart the server.
- **The server keeps no state.** The [Kubernetes operator](integrations/kubernetes/overview.md) acts on contracts in a cluster, and durable evidence lives in an [Evidence Server](evidence.md). The MCP server's state ends with its process.

`--fleet` names its sources the same way `pacto fleet` does:

```bash
pacto mcp --fleet --k8s --oci ghcr.io/acme/payments-api-pacto:2.1.0
```

See [`pacto mcp` reference](cli-reference.md#pacto-mcp) for source flags and [The Pacto Operational Graph](operational-graph.md) for the read model.

## The authoring tools

Every mode except `--root` carries these four.

| Tool | Description |
|------|-------------|
| `pacto_create` | Create a new contract from intent-level inputs. Supports dry run. |
| `pacto_edit` | Edit an existing contract. Supports dry run. |
| `pacto_check` | Validate a contract and return errors, warnings and suggestions. |
| `pacto_schema` | Return the full JSON Schema reference. |

**Structured inputs are JSON-encoded strings.** Every input that carries structure has the MCP wire type `string`, and the value is JSON serialised into a string:

```json
{
  "name": "orders",
  "interfaces": "[{\"name\":\"api\",\"type\":\"openapi\"}]"
}
```

!!! warning
    Passing a real JSON array or object where the JSON-encoded string is expected is a **silent no-op**. The argument is discarded, the call still succeeds, `changes` comes back `null` and nothing is written. Check the result's `changes` and `summary`. `pacto_check`'s own "add an interface" suggestion carries `add_interfaces` as a real array, so an agent that forwards that tool call verbatim hits this case.

**`pacto_check` is `pacto validate`.** It runs the same validation path, including `pacto.lock` verification and recursive `policies[].ref` resolution, so a contract it calls valid is one CI accepts.

**`add_config_properties` writes the schema.** `pacto_edit` merges the properties into the first configuration's local schema file (`configuration/schema.json` when it has to declare one). A property already in the schema is replaced and `required` names are unioned.

### How `pacto_create` reads a description

`description` is matched against fixed keyword lists, case-insensitively and on whole words. A match only fills in what the call left unsaid: explicit `interfaces` or `workload` always win, and every inference is listed in the result's `derived`.

| Keywords | Inferred |
|----------|----------|
| `REST`, `HTTP`, `API`, `web` | a public `openapi` interface named `http-api` |
| `gRPC` | an internal `grpc` interface named `grpc-api` |
| `event`, `kafka`, `nats`, `pubsub`, `queue`, `message`, `rabbit` | an `asyncapi` interface named `events` |
| `worker`, `consumer`, `processor` | a `job` workload |
| `cron`, `scheduled`, `periodic`, `batch` | a `scheduled` workload (wins over `job`) |

Interfaces are inferred only when the call passes none. The match is a keyword heuristic: "no HTTP" still matches `HTTP`.

State is never inferred: without the state flags the contract is stateless. Naming `postgres` declares a database dependency and leaves the service stateless.

## Bundle capabilities

Pass a bundle reference and Pacto turns that bundle's OpenAPI operations into executable agent tools:

```bash
pacto mcp ./my-service --base-url https://api.example.com
```

Each operation becomes an MCP tool named by its `operationId` (or derived as `<method>_<path>`).

**The `pacto_` names are reserved.** A contract declaring `operationId: pacto_check` would take over the authoring tool. Pacto skips such operations.

**Read-only by default.** Only `GET`/`HEAD` operations are exposed unless you pass `--allow-writes`. The live host comes from `--base-url`, falling back to the spec's `servers[0]` URL. When you supply credentials, `--base-url` is required.

**Service authentication (`--auth`).** Credentials are supplied per OpenAPI security scheme with `--auth name=value`:

| Scheme type | How the credential is applied |
|-------------|-------------------------------|
| `apiKey` | Sent as the declared header or query parameter |
| `http` `bearer` (and `oauth2` / `openIdConnect`) | `Authorization: Bearer <value>` |
| `http` `basic` | `Authorization: Basic <value>` (supply pre-encoded `user:pass`) |

**`pacto_skill`.** Bundles may ship domain knowledge as `skills/*.md`. The `pacto_skill` tool lists them and returns their content.

**What a session freezes.** Bundle mode, `--fleet` and `--root` resolve their inputs once at startup. To pick up changes, restart the server. Two exceptions: `pacto_skill` reads from disk on every call, and the authoring tools act on whatever is on disk.

## Catalog mode

`pacto mcp --root <ref>` starts a read-only contract catalog: the roots you name, plus their dependency closure, resolved once at startup and then frozen for the life of the process.

```bash
# One published platform, one contract you are still working on
pacto mcp \
  --root oci://ghcr.io/acme/platform:1.4.0 \
  --root ./experimental-platform
```

`--root` is repeatable and takes either a local bundle directory or an `oci://` reference. Pacto discovers only what you name, plus its dependency closure. It never crawls a registry, guesses repository names or reads a catalog file.

The surface is two MCP *resources* (`pacto://catalog` and `pacto://catalog/closure`) plus one tool (`pacto_catalog_revision`). The catalog resource answers: schema version (`pacto.dev/catalog/v1`), catalog id, generation time, bounds, completeness and every requested root, including roots that did not resolve and why. The closure resource answers: every deduplicated revision with its content identity, rank and retained paths; every resolved dependency edge; every dependency that did not resolve; and every conflict and cycle, reported as found.

Catalog mode registers nothing else, so a server started for read-only discovery cannot modify a contract. A root or a dependency that could not be resolved stays visible with a category such as `NOT_FOUND`, `AUTH_FAILED` or `UNAVAILABLE` in place of the raw registry error, and the whole answer is marked `partial`. Under `partial`, a revision missing from the answer is *unknown*.

## Create a service's files with pactify

`pacto_create` with `pactify: true` does what `pacto init` does for a folder
holding a service: it reads the Dockerfile and Kubernetes files and writes the
contract. `describe_only: true` leaves out the publish section, for a request
to describe or document a service. With `delivery: true` it also writes the
cluster files. `registry`, `environments`, `fleet_roots`,
`no_transparency_log`, `insecure_registry` and `registry_secret` match the CLI
flags, and `dry_run` plans without writing. See
[Pactify a service](pactify.md).

`pacto_create` and `pacto_edit` return `warnings` for a value that validates
but is likely wrong, such as an owner written as `team/billing`.
`pacto_schema` returns the JSON Schema and, under `agents`, how to describe an
AI agent as a service.

## Publish and delivery servers

`--publish` adds `pacto_publish_plan` (what publishing would write and which
checks refuse it, without writing) and `pacto_publish` (publishes the version
the same way `pacto publish` does, with this process's registry credentials,
always to a new tag) beside the authoring tools. It runs over stdio
only.

`--delivery` serves the delivery tools over the cluster your kubeconfig
points at: `pacto_delivery_operation_types`, `pacto_delivery_candidates`,
`pacto_delivery_operation_plan`, `pacto_delivery_get`, `pacto_delivery_explain`,
`pacto_delivery_history`, `pacto_delivery_blame`, `pacto_delivery_undo_plan` and
`pacto_delivery_record_verify` for everyone, plus
`pacto_delivery_operation_propose` and `pacto_delivery_cancel` only when the
cluster says the caller is an agent. Delivery mode has no approve tool.

`pacto_delivery_candidates` lists the versions a Stage may take, with their
digests. The `to` input of plan and propose takes a version (`3.0.0`), a tag or
a digest; a version or a tag must name exactly one of those candidates, or the
tool refuses it and says why. A plan's `next` says what proposing it leads to:
which teams must sign, that an agent cannot answer and the operation is then
Held, and what happens at the approval deadline. `pacto_delivery_get` adds
`next` with the deadline while an operation is held. Propose with the plan's
intent digest and the same `reason` and `on_behalf_of`: the digest binds them,
and `PlanStale` names the one that differs when it can tell. Over HTTP it
needs token exchange; see
[agents over the network](integrations/kubernetes/delivery-identity.md#agents-over-the-network).
