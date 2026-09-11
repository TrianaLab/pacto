# pacto diff and change classification: what counts as a breaking change { #change-classification-rules }

`pacto diff` classifies every detected change using a deterministic rule table. This is what powers breaking change detection in CI pipelines, and the tables below are the complete list — every field Pacto compares and the verdict it reaches.

Each change is classified as:

- **`BREAKING`** — a change that will break consumers or platforms relying on the previous contract
- **`POTENTIAL_BREAKING`** — a change that *may* break consumers depending on how they use the field
- **`NON_BREAKING`** — a safe change that doesn't affect compatibility

Any change not matched by a specific rule below defaults to **POTENTIAL_BREAKING**.

## Service identity

| Field | Change | Classification |
|-------|--------|----------------|
| `service.name` | Modified | **BREAKING** |
| `service.version` | Modified | NON_BREAKING |
| `service.owner.team` | Added / Modified / Removed | NON_BREAKING |
| `service.owner.dri` | Added / Modified / Removed | NON_BREAKING |
| `service.owner.contacts[]` | Added / Modified / Removed | NON_BREAKING |

The owner block is compared field by field, so changing a `dri` or adding a
contact while the `team` is unchanged surfaces the specific change rather than an
opaque whole-owner modification. Contacts are keyed by `type:value`; a `purpose`
change on an existing contact is a modification. `service` carries identity only —
there is no `image` or `chart` field to diff. `pactoVersion` is not diffable
either: `"2.0"` is the only value that loads, so both sides always match and a
version change is a load error rather than a change entry.

## Interfaces

| Field | Change | Classification |
|-------|--------|----------------|
| `interfaces` | Added | NON_BREAKING |
| `interfaces` | Removed | **BREAKING** |
| `interfaces.type` | Modified | **BREAKING** |
| `interfaces.ref` | Modified | POTENTIAL_BREAKING |
| `interfaces.visibility` | Modified | POTENTIAL_BREAKING |

Interfaces are keyed by `name`. The `type` enum is `openapi`/`asyncapi`/`grpc`, and the spec pointer is `ref`.

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
being validated against anything. Introducing one where there was none is
`POTENTIAL_BREAKING` rather than safe, because a configuration nothing checked
before can newly fail validation.

Inline configuration `values` are the provider's own defaults, not part of the
consumer-facing contract surface, so value changes are diffed key by key (e.g.
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
the same reason it is on a configuration: a policy nothing validated before can
newly fail against it.

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

`workload` and `state` are top-level sections. There is no `runtime`, `lifecycle`
or `scaling` section to diff — scheduling and scaling are delivery concerns the
contract does not declare.

## Capabilities

| Field | Change | Classification |
|-------|--------|----------------|
| `capabilities` | Added | NON_BREAKING |
| `capabilities` | Removed | POTENTIAL_BREAKING |

Capabilities (`health`, `metrics`, `extension`) are compared as a set, keyed by
`type` — plus `ref` for an `extension`. Adding a capability is a new signal
(`NON_BREAKING`); removing one loses an observability guarantee
(`POTENTIAL_BREAKING`). Only membership of that set is diffed: `binding` is not
part of the key and is not compared, so re-pointing an existing capability's
`binding.interface` or `binding.path` produces no change entry at all.

## Dependencies

| Field | Change | Classification |
|-------|--------|----------------|
| `dependencies` | Added | NON_BREAKING |
| `dependencies` | Removed | **BREAKING** |
| `dependencies.ref` | Modified | POTENTIAL_BREAKING |
| `dependencies.compatibility` | Modified | POTENTIAL_BREAKING |
| `dependencies.required` | Modified | POTENTIAL_BREAKING |

## OpenAPI

`pacto diff` performs deep comparison of referenced OpenAPI specs, detecting changes at the path, method, parameter, request body, and response level.

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
`BREAKING` because existing clients omit it. Relaxing required to optional is not.

Parameters are identified by `name` + `in` (location: query, path, header, cookie). A parameter renamed or moved to a different location is treated as a removal + addition.

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
both sides is never reported as one opaque modification: it is deep-diffed field
by field and each inner difference is classified by the [JSON Schema
rules](#json-schema-configuration-policy-schemas) below, so there is no such
change for a Modified rule to answer.

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
`required` — most response schemas have no `required` array at all, and deleting
a field consumers read is the commonest way a REST provider breaks them. Every
other inner difference (a property added, a type or constraint changed) is
`POTENTIAL_BREAKING`.

Change paths use a hierarchical format that pinpoints the exact location, for example:

```
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

A channel present on both sides is never reported as one opaque modification. It is compared field by field, so a new payload property, a changed property type or a new `required` entry each surface as their own change, classified by the [JSON Schema rules](#json-schema-configuration-policy-schemas) below: a `required` change is `BREAKING`, every other inner difference is `POTENTIAL_BREAKING`. That is why there is no `asyncapi.channels` Modified row — the engine has no such change to emit.

### Operations

| Field | Change | Classification |
|-------|--------|----------------|
| `asyncapi.operations` | Added | NON_BREAKING |
| `asyncapi.operations` | Removed | **BREAKING** |

An operation present on both sides is deep-diffed exactly like a channel, so flipping `action` from `send` to `receive` surfaces as `asyncapi.operations[sendOrder].action` (`POTENTIAL_BREAKING`) rather than a modification of the operation as a whole.

Top-level `operations` are an AsyncAPI 3.x concept. A 2.x document has no `operations` map, so only its channels are compared.

Only `channels` and `operations` are compared. `info`, `servers`, `components` and `defaultContentType` are ignored for the same reason `metadata` is: they churn on every release without changing what a consumer can publish or subscribe to.

Change paths pinpoint the exact location, for example:

```
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

proto3 has no `required`, so an added rpc, message or field is always wire-compatible with existing clients. Everything else here is `BREAKING`: removing a service, rpc, message or field breaks every caller, and a changed rpc signature (including a switch between unary and streaming) or a field whose type or number changed breaks the wire format for clients built against the old descriptor. That is why a modified field is `BREAKING` rather than `POTENTIAL_BREAKING`.

Change paths pinpoint the exact location, for example:

```
grpc.services[FraudService]
grpc.rpcs[FraudService.EvaluateTransaction]
grpc.messages[EvaluateTransactionRequest].fields[metadata]
```

### What the proto comparison does not do

The comparison is a text scan of proto3 source, not a protobuf compile. In practice:

- **`import`s are not resolved.** Only the declarations in the referenced file are compared. A message that moves into an imported file reads as a removal.
- **Nested messages and `oneof` bodies are not descended into.** Their fields are skipped rather than misread as fields of the enclosing message, so a change inside one produces no change entry.
- **Identity is the declared name.** A renamed field or rpc reads as a removal plus an addition even when the field number is unchanged.
- **Type identity is textual.** The scanner does not resolve names against `package` or `import`, so fully qualifying a type (`Inner` → `pkg.v1.Inner`) reads as a modified field even though the descriptor is unchanged.
- **Inline field options are ignored.** A field's trailing `[...]` block is parsed off and dropped, so adding or removing `[deprecated = true]` is not a change while a retype behind one still is. A field whose option block contains braces (a nested text-format value such as `[(validate.rules).string = {min_len: 1}]`) is skipped entirely, on both sides, so it never appears in the compared surface.
- **Comments and string literals are handled by one pre-scan.** Before anything is matched, the source is walked once and every comment, along with the contents of every *closed* string literal, is blanked to spaces. Newlines survive, so the line structure and every byte offset are unchanged. A `//`, a `}` or a `/*` inside a closed string (a URL in an `option` line, say) is therefore inert: it neither truncates the line, nor closes a block early, nor opens a comment. Both quote styles and backslash escapes are understood, and the single pass settles comment-vs-string precedence, so a `/*` written inside a `//` comment does not open a block comment. A quote with no closing quote before the line break is not a proto string literal at all, since a literal cannot span a raw newline; the scan blanks from that quote to the end of the line, keeping any `;` it finds. The blanking makes the tail's braces and comment openers inert, and the surviving `;` still ends the statement, so the declaration on the next line is read normally.

## JSON Schema (configuration & policy schemas)

Schema files referenced by `configurations[].schema`, `policies[].schema`, or the auto-detected `policy/schema.json` are compared recursively. Every structural difference — properties, types, constraints, defaults, enums, etc. — is detected and classified.

| Field | Change | Classification |
|-------|--------|----------------|
| `schema.properties.*` | Added | POTENTIAL_BREAKING |
| `schema.properties.*` | Removed | POTENTIAL_BREAKING |
| `schema.properties.*` | Modified | POTENTIAL_BREAKING |
| `schema.required` | Added / Removed | **BREAKING** |
| `schema.*` (any other path) | Added / Removed / Modified | POTENTIAL_BREAKING |

The same recursive comparison classifies OpenAPI request bodies and responses,
AsyncAPI payloads and these schema files, but not identically: a `required`
entry is a promise in one direction and an obligation in the other, so the
verdict depends on which side supplies the data.

- **OpenAPI request bodies and responses** carry a known direction and use the
  mirrored table under [Responses](#responses) above.
- **Configuration schemas, policy schemas and AsyncAPI payloads** do not. A
  configuration schema constrains a value the platform supplies; an AsyncAPI
  channel is published by one service and subscribed by another. With no
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

All readiness changes are classified as `NON_BREAKING` — readiness tracks operational
maturity and does not affect runtime compatibility. Adding or removing the whole
`readiness` block surfaces as a single `readiness` change; otherwise the gate
fields (`expires`, `minScore`, `partialCredit`) are compared individually. Claims
are keyed by `id`, and a claim whose `status`, `weight`, `evidence` or any other
field changed is reported as a modification of that claim (e.g.
`readiness.claims[dashboard]`). The `readiness.history` revision log is not diffed:
it is an append-only changelog that changes on every release and would only add
noise.

## Not currently compared

| Section | Status |
|---------|--------|
| `metadata` | Not diffed. Free-form `metadata` keys are carried through to documentation and the dashboard but are ignored by the diff engine. |
| `capabilities[].binding` | Not diffed. Capabilities are keyed by `type` (and `ref`), so a changed `binding.interface` or `binding.path` on an otherwise unchanged capability is invisible to `pacto diff`. |
| `readiness.history[]` | Not diffed. An append-only changelog that changes on every release; see [Readiness](#readiness). |

If you rely on any of these being flagged in CI, gate on them separately (for
example with a policy or a custom check).

## SBOM

SBOM changes are reported separately from contract changes. They are **informational only** and never affect the overall diff classification.

| Change | Description |
|--------|-------------|
| Package added | A new package appears in the SBOM |
| Package removed | A package no longer appears in the SBOM |
| Package version modified | A package's version changed |
| Package license modified | A package's license changed |
| Package supplier modified | A package's supplier changed |
