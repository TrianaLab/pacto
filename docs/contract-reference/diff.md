# pacto diff and change classification: what counts as a breaking change { #change-classification-rules }

`pacto diff` compares two versions of a contract and tells you which changes can break the services that depend on it. Run it in CI to catch a breaking change before it merges:

```bash
pacto diff oci://ghcr.io/acme/payments-api:1.4.0 ./payments-api
```

Every change goes through a fixed rule table, so the same two contracts always give the same answer. The tables below are the complete list: every field Pacto compares and the verdict it reaches.

Each change is classified as:

- **`BREAKING`**: a change that breaks consumers or platforms relying on the previous contract
- **`POTENTIAL_BREAKING`**: a change that *may* break consumers, depending on how they use the field
- **`NON_BREAKING`**: a safe change that keeps compatibility

A change that matches no specific rule below defaults to **POTENTIAL_BREAKING**.

## Service identity

| Field | Change | Classification |
|-------|--------|----------------|
| `service.name` | Modified | **BREAKING** |
| `service.version` | Modified | NON_BREAKING |
| `service.owner.team` | Added / Modified / Removed | NON_BREAKING |
| `service.owner.dri` | Added / Modified / Removed | NON_BREAKING |
| `service.owner.contacts[]` | Added / Modified / Removed | NON_BREAKING |

The owner block is compared field by field, so changing a `dri` or adding a
contact while the `team` is unchanged reports that specific change. Contacts are
keyed by `type:value`, and a `purpose` change on an existing contact is a
modification. `service` carries identity only, so the contract has no `image` or
`chart` field to diff. A `pactoVersion` change, such as `"2.0"` to `"2.1"`, is
`NON_BREAKING`.

## Interfaces

| Field | Change | Classification |
|-------|--------|----------------|
| `interfaces` | Added | NON_BREAKING |
| `interfaces` | Removed | **BREAKING** |
| `interfaces.type` | Modified | **BREAKING** |
| `interfaces.ref` | Modified | POTENTIAL_BREAKING |
| `interfaces.visibility` | Modified | POTENTIAL_BREAKING |
| `interfaces.lifecycle` | Added / Removed | NON_BREAKING |
| `interfaces.lifecycle.deprecated` | Modified | NON_BREAKING |
| `interfaces.lifecycle.sunset` | Added / Removed | NON_BREAKING |
| `interfaces.lifecycle.sunset` | Modified, earlier | POTENTIAL_BREAKING |
| `interfaces.lifecycle.sunset` | Modified, later | NON_BREAKING |
| `interfaces.lifecycle.successor` | Added / Modified / Removed | NON_BREAKING |
| `interfaces.guarantees` | Added | NON_BREAKING |
| `interfaces.guarantees` | Removed | **BREAKING** |
| `interfaces.guarantees.weakened` | Modified | **BREAKING** |
| `interfaces.guarantees.strengthened` | Modified | NON_BREAKING |

Interfaces are keyed by `name`. Deprecating an interface changes nothing a
consumer calls, so it is a notice. An earlier sunset shortens the time consumers
have to migrate. Removing an interface before its sunset is still **BREAKING**,
and the reason says that the sunset has not been reached.

Each [guarantee](sections.md#guarantees) is
compared on its own: a longer `dedupeWindow` or `retention`, a shorter
`timeout`, `global` ordering in place of `per-key` and a new `idempotent: true`
are stronger. A `delivery` change is weaker in both directions, because a
consumer relying on the old value breaks. The `type` enum is `openapi`/`asyncapi`/`grpc`, and the spec pointer is `ref`.

## Configurations

| Field | Change | Classification |
|-------|--------|----------------|
| `configurations[]` | Added | NON_BREAKING |
| `configurations[]` | Removed | **BREAKING** |
| `configurations[].schema` | Added | POTENTIAL_BREAKING |
| `configurations[].schema` | Modified | POTENTIAL_BREAKING |
| `configurations[].schema` | Removed | **BREAKING** |
| `configurations[].ref` | Added | NON_BREAKING |
| `configurations[].ref` | Modified | POTENTIAL_BREAKING |
| `configurations[].ref` | Removed | **BREAKING** |
| `configurations[].values.*` | Added / Modified / Removed | NON_BREAKING |

Dropping a `schema` is `BREAKING`: the configuration a consumer supplies stops
being validated. Introducing a schema where there was none is
`POTENTIAL_BREAKING`, because a configuration that was unchecked before can now
fail validation.

Inline configuration `values` are the provider's own defaults and sit outside the
consumer-facing contract surface. Value changes are diffed key by key (for example
`configurations[app].values.replicas`) and classified `NON_BREAKING`.

## Policy

| Field | Change | Classification |
|-------|--------|----------------|
| `policies[]` | Added | NON_BREAKING |
| `policies[]` | Removed | POTENTIAL_BREAKING |
| `policies[].schema` | Added / Modified / Removed | POTENTIAL_BREAKING |
| `policies[].ref` | Added | NON_BREAKING |
| `policies[].ref` | Modified | POTENTIAL_BREAKING |
| `policies[].ref` | Removed | POTENTIAL_BREAKING |

Introducing a policy `schema` where there was none is `POTENTIAL_BREAKING` for
the same reason it is on a configuration: a contract that no policy checked before
can now fail against it.

## Workload and state

| Field | Change | Classification |
|-------|--------|----------------|
| `workload` | Modified | **BREAKING** |
| `workload` | Added / Removed | NON_BREAKING |
| `state.type` | Modified | **BREAKING** |
| `state.persistence.scope` | Modified | **BREAKING** |
| `state.persistence.durability` | Modified | **BREAKING** |
| `state.dataCriticality` | Modified | POTENTIAL_BREAKING |
| `state.dataCriticality` | Added / Removed | NON_BREAKING |

`workload` and `state` are top-level sections. Scheduling and scaling are
delivery concerns, so the contract has no `runtime`, `lifecycle` or `scaling`
section to diff.

## Capabilities

| Field | Change | Classification |
|-------|--------|----------------|
| `capabilities` | Added | NON_BREAKING |
| `capabilities` | Removed | POTENTIAL_BREAKING |

Capabilities (`health`, `metrics`, `extension`) are compared as a set, keyed by
`type`, plus `ref` for an `extension`. Adding a capability is a new signal
(`NON_BREAKING`). Removing one loses an observability guarantee
(`POTENTIAL_BREAKING`). The diff compares only set membership and ignores
`binding`, so re-pointing an existing capability's `binding.interface` or
`binding.path` produces no change entry.

## Dependencies

| Field | Change | Classification |
|-------|--------|----------------|
| `dependencies` | Added | NON_BREAKING |
| `dependencies` | Removed | **BREAKING** |
| `dependencies.ref` | Modified | POTENTIAL_BREAKING |
| `dependencies.compatibility` | Modified | POTENTIAL_BREAKING |
| `dependencies.required` | Modified | POTENTIAL_BREAKING |
| `dependencies.requires` | Added / Modified | POTENTIAL_BREAKING |
| `dependencies.requires` | Removed | NON_BREAKING |

## OpenAPI

`pacto diff` performs deep comparison of referenced OpenAPI specs, detecting changes at the path, method, parameter, request body and response level.

### Paths

| Field | Change | Classification |
|-------|--------|----------------|
| `openapi.paths` | Added | NON_BREAKING |
| `openapi.paths` | Removed | **BREAKING** |

### Methods

| Field | Change | Classification |
|-------|--------|----------------|
| `openapi.methods` | Added | NON_BREAKING |
| `openapi.methods` | Removed | **BREAKING** |

### Parameters

| Field | Change | Classification |
|-------|--------|----------------|
| `openapi.parameters` | Added (optional) | POTENTIAL_BREAKING |
| `openapi.parameters` | Added (`required: true`) | **BREAKING** |
| `openapi.parameters` | Removed | **BREAKING** |
| `openapi.parameters` | Modified (optional → required) | **BREAKING** |
| `openapi.parameters` | Modified (any other) | POTENTIAL_BREAKING |

Requiring a parameter is the one parameter change that escalates: introducing a new
`required` parameter, or flipping an existing one from optional to required, is
`BREAKING` because existing clients omit it. Relaxing required to optional is
`POTENTIAL_BREAKING`.

Parameters are identified by `name` + `in` (location: query, path, header, cookie). A parameter renamed or moved to a different location counts as a removal plus an addition.

### Request body

| Field | Change | Classification |
|-------|--------|----------------|
| `openapi.request-body` | Added | POTENTIAL_BREAKING |
| `openapi.request-body` | Removed | POTENTIAL_BREAKING |

### Responses

| Field | Change | Classification |
|-------|--------|----------------|
| `openapi.responses` | Added | NON_BREAKING |
| `openapi.responses` | Removed | **BREAKING** |

Neither table has a Modified row. A request body or a status code present on
both sides is deep-diffed field by field, and the [JSON Schema
rules](#json-schema-configuration-policy-schemas) below classify each inner
difference.

Which side of the exchange the body belongs to changes the verdict, because a
`required` entry means the opposite thing on each side:

| Where | Change | Classification | Why |
|-------|--------|----------------|-----|
| request body | `required` field added | **BREAKING** | existing callers omit it |
| request body | `required` field removed | NON_BREAKING | the caller's obligation relaxed |
| response | `required` field added | NON_BREAKING | a stronger guarantee to the reader |
| response | `required` field removed | **BREAKING** | a guarantee consumers read is withdrawn |
| response | property removed | **BREAKING** | data a consumer could read is gone |

A property removed from a response is `BREAKING` whether or not it was listed in
`required`. Most response schemas have no `required` array at all, and deleting
a field consumers read is the commonest way a REST provider breaks them. Every
other inner difference (a property added, a type or constraint changed) is
`POTENTIAL_BREAKING`.

Change paths use a hierarchical format that pinpoints the exact location, for example:

```text
openapi.paths[/users].methods[GET].parameters[filter:query]
openapi.paths[/users].methods[POST].request-body.content.application/json.schema.required[email]
openapi.paths[/users].methods[GET].responses[200].content.application/json.schema.properties.email
```

## AsyncAPI

`pacto diff` compares referenced AsyncAPI documents at the channel and operation level. Both AsyncAPI 2.x and 3.x are supported, in YAML or JSON.

### Channels

| Field | Change | Classification |
|-------|--------|----------------|
| `asyncapi.channels` | Added | NON_BREAKING |
| `asyncapi.channels` | Removed | **BREAKING** |

A channel present on both sides is compared field by field, so a new payload property, a changed property type or a new `required` entry each surface as their own change. The [JSON Schema rules](#json-schema-configuration-policy-schemas) below classify each one: a `required` change is `BREAKING` and every other inner difference is `POTENTIAL_BREAKING`. For that reason the table has no `asyncapi.channels` Modified row.

### Operations

| Field | Change | Classification |
|-------|--------|----------------|
| `asyncapi.operations` | Added | NON_BREAKING |
| `asyncapi.operations` | Removed | **BREAKING** |

An operation present on both sides is deep-diffed exactly like a channel, so flipping `action` from `send` to `receive` surfaces as `asyncapi.operations[sendOrder].action` (`POTENTIAL_BREAKING`).

Top-level `operations` are an AsyncAPI 3.x concept. A 2.x document has no `operations` map, so only its channels are compared.

Only `channels` and `operations` are compared. The diff ignores `info`, `servers`, `components` and `defaultContentType` for the same reason it ignores `metadata`: they change on most releases and leave what a consumer can publish or subscribe to unchanged.

Change paths pinpoint the exact location, for example:

```text
asyncapi.channels[payment.completed]
asyncapi.channels[payment.refunded].publish.message.payload.required[charge_id]
asyncapi.operations[sendOrder].action
```

## gRPC

`pacto diff` compares referenced `.proto` files at the service, rpc, message and field level.

| Field | Change | Classification |
|-------|--------|----------------|
| `grpc.services` | Added | NON_BREAKING |
| `grpc.services` | Removed | **BREAKING** |
| `grpc.rpcs` | Added | NON_BREAKING |
| `grpc.rpcs` | Removed | **BREAKING** |
| `grpc.rpcs` | Modified | **BREAKING** |
| `grpc.messages` | Added | NON_BREAKING |
| `grpc.messages` | Removed | **BREAKING** |
| `grpc.messages.fields` | Added | NON_BREAKING |
| `grpc.messages.fields` | Removed | **BREAKING** |
| `grpc.messages.fields` | Modified | **BREAKING** |

proto3 has no `required`, so an added rpc, message or field is always wire-compatible with existing clients. Everything else here is `BREAKING`. Removing a service, rpc, message or field breaks every caller. A changed rpc signature (including a switch between unary and streaming), or a field whose type or number changed, breaks the wire format for clients built against the old descriptor. For that reason a modified field is `BREAKING`.

Change paths pinpoint the exact location, for example:

```text
grpc.services[FraudService]
grpc.rpcs[FraudService.EvaluateTransaction]
grpc.messages[EvaluateTransactionRequest].fields[metadata]
```

### Limits of the proto comparison

The comparison scans proto3 source as text. It runs no protobuf compiler, which has these effects:

- **`import`s stay unresolved.** Pacto compares only the declarations in the referenced file. A message that moves into an imported file reads as a removal.
- **Nested messages and `oneof` bodies are skipped.** Their fields are left out of the enclosing message, so a change inside one produces no change entry.
- **Identity is the declared name.** A renamed field or rpc reads as a removal plus an addition even when the field number is unchanged.
- **Type identity is textual.** The scanner compares names as written, without `package` or `import` resolution, so fully qualifying a type (`Inner` → `pkg.v1.Inner`) reads as a modified field even though the descriptor is unchanged.
- **Inline field options are ignored.** Adding or removing `[deprecated = true]` produces no change entry, while a retype behind one still does. A field whose option block contains braces (a nested text-format value such as `[(validate.rules).string = {min_len: 1}]`) is dropped from the revision that carries it, and only from that one. Adding such an option to an existing field therefore reads as a removal and is classified **BREAKING**.
- **Comments and string literals are inert.** A `//`, a `}` or a `/*` inside a string literal, such as a URL in an `option` line, is read as part of the string.

## JSON Schema (configuration & policy schemas)

Schema files referenced by `configurations[].schema`, `policies[].schema` or the auto-detected `policy/schema.json` are compared recursively. Pacto detects and classifies every structural difference, including properties, types, constraints, defaults and enums.

| Field | Change | Classification |
|-------|--------|----------------|
| `schema.properties.*` | Added | POTENTIAL_BREAKING |
| `schema.properties.*` | Removed | POTENTIAL_BREAKING |
| `schema.properties.*` | Modified | POTENTIAL_BREAKING |
| `schema.required` | Added / Removed | **BREAKING** |
| `schema.*` (any other path) | Added / Removed / Modified | POTENTIAL_BREAKING |

The same recursive comparison classifies OpenAPI request bodies and responses,
AsyncAPI payloads and these schema files, with one difference. A `required`
entry is a promise in one direction and an obligation in the other, so the
verdict depends on which side supplies the data.

- **OpenAPI request bodies and responses** carry a known direction and use the
  mirrored table under [Responses](#responses) above.
- **Configuration schemas, policy schemas and AsyncAPI payloads** carry no
  direction. A configuration schema constrains a value the platform supplies,
  and one service publishes to an AsyncAPI channel while another subscribes. With no
  direction to read, both `required` transitions take the conservative answer
  and stay `BREAKING`, and a removed property is `POTENTIAL_BREAKING` like any
  other structural difference.

## Readiness

| Field | Change | Classification |
|-------|--------|----------------|
| `readiness` | Added / Removed | NON_BREAKING |
| `readiness.expires` | Added / Modified / Removed | NON_BREAKING |
| `readiness.minScore` | Added / Modified / Removed | NON_BREAKING |
| `readiness.partialCredit` | Added / Modified / Removed | NON_BREAKING |
| `readiness.claims[]` | Added / Modified / Removed | NON_BREAKING |

All readiness changes are `NON_BREAKING`, because readiness tracks operational
maturity and leaves runtime compatibility unchanged. Adding or removing the whole
`readiness` block surfaces as a single `readiness` change; otherwise the gate
fields (`expires`, `minScore`, `partialCredit`) are compared individually. Claims
are keyed by `id`, and a claim whose `status`, `weight`, `evidence` or any other
field changed is reported as a modification of that claim (for example
`readiness.claims[dashboard]`). The diff skips the `readiness.history` revision
log, an append-only changelog that changes on every release.

## Fields the diff skips

| Section | Behavior |
|---------|--------|
| `metadata` | Skipped. Free-form `metadata` keys appear in documentation and the dashboard. |
| `capabilities[].binding` | Skipped. Capabilities are keyed by `type` (and `ref`), so `pacto diff` reports no change for a new `binding.interface` or `binding.path` on an otherwise unchanged capability. |
| `readiness.history[]` | Skipped. An append-only changelog that changes on every release; see [Readiness](#readiness). |

To flag changes to these fields in CI, gate on them separately, for example with
a policy or a custom check.

## SBOM

SBOM changes are reported separately from contract changes. They are **informational** and leave the overall diff classification unchanged.

| Change | Description |
|--------|-------------|
| Package added | A new package appears in the SBOM |
| Package removed | A package left the SBOM |
| Package version modified | A package's version changed |
| Package license modified | A package's license changed |
| Package supplier modified | A package's supplier changed |
