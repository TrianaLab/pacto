# The authoring tools

These four are the default server, and every mode except `--root` carries them
as well.

| Tool | Description |
|------|-------------|
| `pacto_create` | Create a new contract from intent-level inputs (name, description, interfaces, runtime semantics). Supports dry run. |
| `pacto_edit` | Edit an existing contract — add/remove interfaces and dependencies, change runtime, update metadata. Supports dry run. |
| `pacto_check` | Validate a contract and return errors, warnings, and actionable improvement suggestions. |
| `pacto_schema` | Return the Pacto format explanation and full JSON Schema reference. Call this first if the assistant needs schema details. |

## Structured inputs are JSON-encoded strings

Every authoring-tool input that carries structure has the MCP wire type `string`,
and the value is JSON *serialised into a string* — not a JSON array or object.
That is true of `interfaces`, `dependencies`, `config_properties` and `metadata`
on `pacto_create`, and of `add_interfaces`, `remove_interfaces`,
`add_dependencies`, `remove_dependencies`, `add_config_properties`,
`set_metadata` and `remove_metadata` on `pacto_edit`:

```json
{
  "name": "orders",
  "interfaces": "[{\"name\":\"api\",\"type\":\"openapi\"}]"
}
```

!!! warning
    Passing a real JSON array or object where the JSON-encoded string is expected
    is a **silent no-op**. The argument is discarded, the call still succeeds,
    `changes` comes back `null` and nothing is written — an absent error is
    therefore not evidence that the edit happened: check the result's `changes`
    and `summary`.

## pacto_create

Creates a new Pacto contract from structured input.

**Key inputs:**

- `name` (required) — service name
- `description` — natural-language description (triggers automatic inference of interfaces and runtime)
- `interfaces` — [JSON-encoded](#structured-inputs-are-json-encoded-strings) array of `{name, type, visibility?}` objects. `type` is one of `openapi`, `asyncapi` or `grpc` — the only three the [contract schema](contract-reference/sections.md#interfaces) allows. There is no `ref` input: the contract's required `interfaces[].ref` is derived as `interfaces/<name>.yaml`.
- `stores_data`, `data_survives_restart`, `data_shared_across_instances` — intent-level runtime flags mapped to contract primitives
- `dry_run` — validate and return the result without writing files

**Description inference:** When a description mentions terms like "REST API" or "gRPC", the tool infers the matching interface; a datastore term like `postgres` or `redis` flips the runtime to stateful; and a messaging term like `kafka` adds an `asyncapi` interface. Matching is case-insensitive but **whole-word**, so `Postgres` and `postgres` are recognised while `PostgreSQL` is not. Dependencies are never inferred — declare them explicitly via the `dependencies` input. Explicit inputs always override inferred values.

**Runtime mapping:** Intent-level flags are deterministically mapped to contract primitives:

| Intent | Contract field |
|--------|---------------|
| `stores_data=true` + `data_survives_restart=false` | `state.type: stateful`, `persistence.durability: ephemeral`, `dataCriticality: medium` |
| `stores_data=true` + `data_survives_restart=true` | `state.type: stateful`, `persistence.durability: persistent` |
| `data_shared_across_instances=true` | `persistence.scope: shared` |
| `data_loss_impact=high` | `dataCriticality: high` |

The persistence rows take effect only when `stores_data=true` — `stores_data` is what sets `state.type: stateful` and the default `dataCriticality: medium`. With `stores_data=false` the state stays stateless, local and ephemeral, and `data_shared_across_instances` is ignored; `data_loss_impact` still sets `dataCriticality` independently of `stores_data`. See [Contract reference](contract-reference/index.md) for the full workload and state field definitions.

## pacto_edit

Modifies an existing contract. Reads the current `pacto.yaml`, applies changes, validates the result, and writes back atomically.

**Key inputs:**

- `path` — directory containing `pacto.yaml` (defaults to `.`)
- `add_interfaces` / `remove_interfaces` — add or remove interfaces. `add_interfaces` takes the same [JSON-encoded](#structured-inputs-are-json-encoded-strings) `{name, type, visibility?}` objects as `pacto_create`; `remove_interfaces` takes a JSON-encoded array of interface names.
- `add_dependencies` / `remove_dependencies` — add or remove dependencies
- Runtime flags (`stores_data`, `data_survives_restart`, etc.)
- `dry_run` — validate without writing

!!! warning "`pacto_edit` can write a bundle that `pacto validate` rejects"
    `pacto_edit` scaffolds a stub spec file only for `openapi` and `grpc`
    interfaces. An `asyncapi` interface is added to `pacto.yaml` with a derived
    `ref: interfaces/<name>.yaml` and **no file is created**, so the tool reports
    success — `changes: ["added interface …"]` — while the referenced file is
    missing, and `pacto validate` then exits non-zero with `FILE_NOT_FOUND`. The
    tool's "validates the result before writing" does not catch this: it validates
    an in-memory bundle in which every missing `ref` is substituted with a stub, so
    the check passes against a filesystem that is not the one written to disk.
    Create the AsyncAPI document yourself after the edit.

## pacto_check

**Output includes:**

- `valid` — whether the contract passes validation
- `errors` / `warnings` — validation issues with path, code, and message
- `summary` — parsed contract overview (name, version, interfaces, runtime state)
- `suggestions` — improvements for a contract that is already valid. There are
  four, each fired by an absent section: no interfaces, no `state`, no
  configuration, no dependencies. Only the first carries a `toolCall` — a sketch
  of the `pacto_edit` call that adds an `openapi` interface; the other three are
  prose. An invalid contract returns no suggestions at all — fix the errors first.

!!! warning "Re-encode the `toolCall` before you pass it on"
    The suggestion's `add_interfaces` value is a real JSON array:
    `{"tool": "pacto_edit", "params": {"add_interfaces": [{"name": "http-api", "type": "openapi"}]}}`.
    `pacto_edit` expects that argument as a **JSON-encoded string**, so forwarding
    the suggestion verbatim is the
    [silent no-op](#structured-inputs-are-json-encoded-strings) documented above.
    Serialise the array first — `"[{\"name\":\"http-api\",\"type\":\"openapi\"}]"` — and the
    same call adds the interface and scaffolds its spec file.

## pacto_schema

Returns the Pacto format description and the full JSON Schema for `pacto.yaml` — a useful first call before creating or editing.
