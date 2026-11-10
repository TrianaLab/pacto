# MCP reference

This page lists the tools, inputs and limits of `pacto mcp`. For a
walkthrough, start with [Drive Pacto from an agent](mcp-integration.md).

## Three tool families and their boundaries

| Family | Tools | What they do | Safety boundary |
|--------|-------|--------------|-----------------|
| **Authoring** | `pacto_create`, `pacto_edit`, `pacto_check`, `pacto_schema` | Create, edit and validate Pacto *contracts*. | Operate on contract files only. `pacto_edit` writes only after validation. |
| **Generated service** | Derived per operation from a bundle's OpenAPI interfaces (`getUser`, `createRefund`, …) | Invoke the *live service* the contract describes. | Read-only (`GET`/`HEAD`) unless you pass `--allow-writes`; every call has a 30-second timeout and follows no redirect, so a 3xx comes back to the agent as the result. |
| **Fleet query** | `pacto_fleet_search`, `pacto_fleet_get`, `pacto_fleet_graph`, `pacto_fleet_status`, `pacto_fleet_explain`, `pacto_fleet_entities`, `pacto_fleet_entity`, `pacto_fleet_attention`, `pacto_fleet_neighborhood`, `pacto_fleet_versions`, [`pacto_impact`](impact.md#the-mcp-tool-and-the-dashboard) | Read-only queries over the *operational system*: services, revisions, targets, owners, business domains, offerings, relationships and what needs attention. `pacto_impact` projects a contract diff onto that system to report which consumers a change affects. `pacto_fleet_get` returns each revision's bundle signature check and OCI referrers as `provenance`. `pacto_fleet_entities` takes `consumes` to list the services that use an [offering](platform-offerings.md#consumers). | Always read-only: they write nothing. They still make network calls, and the server that hosts them may also register write tools; see [Fleet query safety](#fleet-query-safety). Catalog mode is the only server with no write tool. |

Outside these families: `pacto_skill` (a bundle's skill files) and `pacto_catalog_revision` (one revision from the catalog, see [Catalog mode](#catalog-mode)). The publish and delivery tools are listed [below](#publish-and-delivery-servers).

These tasks are CLI-only: inspecting a registry contract, resolving a dependency
graph, diffing revisions, generating docs and the two non-query `pacto fleet`
operations, `reconcile` (declared dependencies against observed traffic) and
`snapshot` (the whole read model as one document). Only `pacto_publish` pushes,
and no tool deploys. In delivery mode an agent asks for a change; Pacto's
checks and people decide. [Server modes](cli-reference.md#server-modes) lists which tools each
invocation registers.

!!! warning "Tools carry no safety annotations"
    Pacto ships **no MCP tool annotations**. A `tools/list` response carries no
    [`annotations`](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#tool-annotations)
    member (`readOnlyHint`, `destructiveHint` or `idempotentHint`) on any tool,
    including `pacto_create`, `pacto_edit` and a generated
    tool such as `createRefund`, which moves money. Build allow-lists by hand
    from the tool name. `pacto_check`, `pacto_schema`, `pacto_skill`, `pacto_fleet_*`,
    `pacto_impact` and `pacto_catalog_revision` are read-only; `pacto_create` and
    `pacto_edit` write contract files. Under `--allow-writes`, treat every
    generated tool (no `pacto_` prefix) as able to change the live service. A
    bundle cannot claim a `pacto_` name.

### Fleet query safety

- **They are read-only.** They project the [Pacto Operational Graph](operational-graph.md) and write nothing. Every fleet tool, `pacto_impact` included, answers from one shared snapshot. `pacto_impact` also resolves both refs on every call with the server's credentials. When no source holds the changed service, it rebuilds the snapshot with the new revision added, reaching every source (your cluster too if `--k8s` is on).
- **They expose knowledge only.** They never grant, scope or revoke a permission.
- **Partial or stale results are incomplete knowledge.** Every answer carries an `asOf` time, a `completeness` value and structured `limitations`. Branch on those before trusting an answer.
- **Under partial coverage, a missing result means unknown.** If a source was unavailable, "not found" means "not known here".
- **The snapshot refreshes on demand.** A call 30 seconds or more after the last rebuild attempt rebuilds it first, reaching your sources again. Calls inside that interval share one `snapshotId`; a failed rebuild keeps the last good snapshot.
- **The server keeps no state.** Durable evidence lives in an [Evidence Server](evidence.md); the MCP server's state ends with its process.

`--fleet` names its sources the same way `pacto fleet` does; see the [`pacto mcp` reference](cli-reference.md#pacto-mcp):

```bash
pacto mcp --fleet --k8s --oci ghcr.io/acme/payments-api-pacto:2.1.0
```

`--mira-source <url>` adds the calls [Mira](investigate-a-problem.md) saw.

### Link answers to the dashboard

With `--dashboard-url <url>`, each entity in a `pacto_fleet_*` answer except
`pacto_fleet_get` and `pacto_fleet_explain` carries an `href` to its page on
that dashboard, as does each contracted `pacto_impact` consumer.

## The authoring tools

Every mode except `--root` carries these four.

| Tool | Description |
|------|-------------|
| `pacto_create` | Create a new contract from intent-level inputs. Supports dry run. |
| `pacto_edit` | Edit an existing contract, including its configuration references, environments and their values and the service lifecycle. Supports dry run. |
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

**`pacto_check` is `pacto validate`.** It runs the same validation, including `pacto.lock` verification and recursive `policies[].ref` resolution, so a contract it accepts passes CI. Its result carries the same `stages` and `next` as `pacto validate --output-format json`, and each error carries `line` and `column` when Pacto can locate it.

**`add_config_properties` writes the schema.** `pacto_edit` merges the properties into the first configuration's local schema file (`configuration/schema.json` when it has to declare one). A property already in the schema is replaced and `required` names are unioned.

**`pacto_edit` edits configuration references, environments and the lifecycle:**

- `add_configuration_refs` adds references to published contracts whose configuration the service takes. Each entry's `configuration` picks one of the referenced contract's configurations and `values` holds the service's values for it. Either field raises `pactoVersion` to 2.4.
- `remove_configuration_refs` removes references by name or ref, and removes their values from every environment file.
- `add_environments` declares environments and creates each missing file holding `{}`. It raises `pactoVersion` to 2.3.
- `set_environment_values` merges values into environment files. Objects merge key by key and a `null` removes its key.
- `lifecycle` sets `service.lifecycle` (`deprecated`, `sunset`, `successor`), and `{"clear": true}` removes it. Setting it raises `pactoVersion` to 2.4.
- `template` sets [`service.template`](contract-reference/sections.md#golden-path-template), which makes the contract a golden path: `{"plugins": [...]}` replaces it, an empty `plugins` list writes `template: {}` and `{"clear": true}` removes it. Either raises `pactoVersion` to 2.4. No input sets `service.origin`: only creation from a golden path writes it.

**Value edits are checked first.** Adding configuration references or environments, or setting environment values, writes only after `pacto_check` passes.

### How `pacto_create` reads a description

`description` is matched against fixed keyword lists, case-insensitively and on whole words. A match only fills in what the call left unsaid: explicit `interfaces` or `workload` always win, and every inference is listed in the result's `derived`.

| Keywords | Inferred |
|----------|----------|
| `REST`, `HTTP`, `API`, `web` | a public `openapi` interface named `http-api` |
| `gRPC` | an internal `grpc` interface named `grpc-api` |
| `event`, `kafka`, `nats`, `pubsub`, `queue`, `message`, `rabbit` | an `asyncapi` interface named `events` |
| `worker`, `consumer`, `processor` | a `job` workload |
| `cron`, `scheduled`, `periodic`, `batch` | a `scheduled` workload (wins over `job`) |

The match is a keyword heuristic: "no HTTP" still matches `HTTP`. State is never inferred: naming `postgres` declares a database dependency and leaves the service stateless.

## Bundle capabilities

Pass a bundle reference and Pacto turns that bundle's OpenAPI operations into executable agent tools:

```bash
pacto mcp ./my-service --base-url https://api.example.com
```

Each operation becomes a tool named by its `operationId` (or `<method>_<path>`).

**The `pacto_` names are reserved.** A contract declaring `operationId: pacto_check` would take over the authoring tool. Pacto skips such operations.

**Read-only by default.** Only `GET`/`HEAD` operations are exposed unless you pass `--allow-writes`. The live host comes from `--base-url`, falling back to the spec's `servers[0]` URL. When you supply credentials, `--base-url` is required.

**Service authentication (`--auth`).** Credentials are supplied per OpenAPI security scheme with `--auth name=value`:

| Scheme type | How the credential is applied |
|-------------|-------------------------------|
| `apiKey` | Sent as the declared header or query parameter |
| `http` `bearer` (and `oauth2` / `openIdConnect`) | `Authorization: Bearer <value>` |
| `http` `basic` | `Authorization: Basic <value>` (supply pre-encoded `user:pass`) |

**`pacto_skill`** lists a bundle's `skills/*.md` files and returns their content.

**What a session freezes.** Bundle mode and `--root` resolve their inputs once at startup; restart to pick up changes. Two exceptions: `pacto_skill` reads from disk on every call, and the authoring tools act on whatever is on disk.

## Catalog mode

`pacto mcp --root <ref>` starts a read-only contract catalog: the roots you name, plus their dependency closure, resolved once at startup and then frozen for the life of the process.

```bash
# One published platform, one contract you are still working on
pacto mcp \
  --root oci://ghcr.io/acme/platform:1.4.0 \
  --root ./experimental-platform
```

`--root` is repeatable and takes a local bundle directory or an `oci://` reference. It never crawls a registry, guesses repository names or reads a catalog file.

The surface is two MCP *resources* (`pacto://catalog` and `pacto://catalog/closure`) plus one tool (`pacto_catalog_revision`). The catalog resource carries the schema version (`pacto.dev/catalog/v1`), catalog id, generation time, bounds, completeness and every requested root, resolved or not and why. The closure resource carries every deduplicated revision (content identity, rank and retained paths), every dependency edge and every conflict and cycle.

Catalog mode registers nothing else, so it cannot modify a contract. A root or a dependency that could not be resolved stays visible with a category such as `NOT_FOUND`, `AUTH_FAILED` or `UNAVAILABLE` in place of the raw registry error, and the whole answer is marked `partial`. Under `partial`, a revision missing from the answer is *unknown*.

## Create a service's files with pactify

`pacto_create` with `pactify: true` does what `pacto init` does for a folder
holding a service: it reads the Dockerfile and Kubernetes files and writes the
contract. `describe_only: true` leaves out the publish section, for a request
to describe a service. With `delivery: true` it also writes the
cluster files. `domain`, `registry`, `environments`, `fleet_roots`,
`no_transparency_log`, `insecure_registry` and `registry_secret` match the CLI
flags, and `dry_run` plans without writing. See
[Pactify a service](pactify.md).

Without `pactify`, `pacto_create` refuses a folder that already holds
`pacto.yaml` (change that contract with `pacto_edit`) and leaves any interface
or schema file already there as it is, listed in `kept`.

## Create a service from a golden path

- `pacto_create` with `from` creates a service from a
  [golden path](golden-paths.md), like `pacto init --from`.
- `from` is an `oci://` reference or a directory. `name` and `owner` are
  required; `domain`, `path`, `registry`, `describe_only`, `delivery`,
  `environments`, `registry_secret`, `insecure_registry` and `dry_run` match
  the CLI flags.
- `values` is a JSON object merged over the path's contract and `set` a JSON
  array of `key=value` overrides, like `--values` and `--set`.
- `run_plugins` is a JSON array of plugin names, each one the path lists.
  Nothing else runs. A `dry_run` lists each plugin and the binary it runs.
- `from` refuses the intent inputs, such as `interfaces`, and `pactify`,
  `fleet_roots` and the cluster inputs.
- `from` serves stdio only. Over HTTP every caller would pull with the
  server's registry credentials, read its directories and write to its paths.
- `pacto_fleet_entities` with `template: only` lists golden paths.

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
points at: `pacto_delivery_status`, `pacto_delivery_operation_types`, `pacto_delivery_candidates`,
`pacto_delivery_operation_plan`, `pacto_delivery_get`, `pacto_delivery_explain`,
`pacto_delivery_history`, `pacto_delivery_blame`, `pacto_delivery_undo_plan`,
`pacto_delivery_record_get`, `pacto_delivery_record_verify` and
`pacto_delivery_lifecycle` for everyone, plus
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

`pacto_delivery_status` takes a `service` (and optionally `domain`,
`namespace` and `why`) and returns one row per Stage of that service you can
see, in promotion order from each Stage's `spec.candidates`, with `asOf` and
`completeness`. Each row names its upstream, the version and contract it runs,
its state as the dashboard board counts it, its newest re-check and its
offers: promote, unfreeze, freeze and undo, each with whether you may ask for
it and why. With `why` it also plans the promote of each candidate (at most 16
per answer) and proposes nothing. On a person's server, which registers no
propose tool, the `note` says so.

`pacto_delivery_history` takes a window (`since`, `until`, each an RFC 3339
time or a duration before now such as `2h`), `cause` and `correlation_id`
filters that narrow the timeline too and, without a `stage`, a `service` and
`domain` filter.

`pacto_delivery_record_get` takes a `namespace`, a `stage` and the `record` id
a history line names, and returns that record: the change, who asked and
approved, every verdict with its citation and how to run it again, and whether
its signature was checked. A record the recorder wrote that does not verify is
never returned; history lists it as `unverifiedRecord`.

`pacto_delivery_lifecycle` takes a `service` and exactly one of `digest`,
`version` or `contract`, with optional `domain` and `namespace`, and follows
that artifact through every Stage of the service you can see: whether it was
published (`signature`, with the Stages behind it) and, per Stage, its events
(`arrived`, `left` or `attempted`), whether it verified or failed there (`yes`,
`no` or `unknown`) and its newest re-check. A Stage whose history is partial
never reads as "never reached".
