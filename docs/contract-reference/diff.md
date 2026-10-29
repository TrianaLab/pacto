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

A change that matches no specific rule below defaults to **POTENTIAL_BREAKING**. Inside an interface schema the default is **BREAKING**; see [Schema position](#schema-position).

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
| `interfaces.compatibilityEpoch` | Modified (raised or lowered) | **BREAKING** |

Interfaces are keyed by `name`. The `type` enum is `openapi`/`asyncapi`/`grpc`/`mcp`, and the spec pointer is `ref`. Deprecating an interface changes nothing a
consumer calls, so it is a notice. An earlier sunset shortens the time consumers
have to migrate. Removing an interface before its sunset is still **BREAKING**,
and the reason says that the sunset has not been reached.

Each [guarantee](sections.md#guarantees) is compared on its own: a longer `dedupeWindow` or `retention`, a shorter
`timeout`, `global` ordering in place of `per-key` and a new `idempotent: true`
are stronger. A `delivery` change is weaker in both directions, because a
consumer relying on the old value breaks.

A [`compatibilityEpoch`](sections.md#compatibility-epoch) change marks a behavior change that no spec diff can see.

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
| `openapi.parameters` | Modified (schema narrowed) | **BREAKING** |
| `openapi.parameters` | Modified (any other) | POTENTIAL_BREAKING |

Introducing a new `required` parameter, or flipping an existing one from
optional to required, is `BREAKING` because existing clients omit it. A
parameter's `schema` sits in [request position](#schema-position), so narrowing
it, for example dropping an `enum` value or lowering a `maxLength`, is
`BREAKING` too. Relaxing required to optional is `POTENTIAL_BREAKING`.

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
both sides is deep-diffed field by field. A request body schema sits in request
position and a response schema in response position, and the
[schema position rules](#schema-position) classify each inner difference. A
documentation edit, such as a response `description` or an `example`, is
`NON_BREAKING`. Any other change around the schema, such as a media type or a
header, is `POTENTIAL_BREAKING`, except that removing a response's whole
`content`, one of its media types or its schema is `BREAKING`.

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

A channel present on both sides is compared field by field, so a new payload property, a changed property type or a new `required` entry each surface as their own change. Old and new publishers and subscribers run side by side, so everything inside a channel sits in [record position](#schema-position): adding an optional payload property and editing documentation are `NON_BREAKING`, and so is adding a 2.x `publish` or `subscribe` operation. Every other inner difference is `BREAKING`. For that reason the table has no `asyncapi.channels` Modified row.

### Operations

| Field | Change | Classification |
|-------|--------|----------------|
| `asyncapi.operations` | Added | NON_BREAKING |
| `asyncapi.operations` | Removed | **BREAKING** |

An operation present on both sides is deep-diffed exactly like a channel, so flipping `action` from `send` to `receive` surfaces as `asyncapi.operations[sendOrder].action` (`BREAKING`).

Top-level `operations` are an AsyncAPI 3.x concept. A 2.x document has no `operations` map, so only its channels are compared.

The diff ignores `info`, `servers` and `defaultContentType`, which change on most releases and leave what a consumer can publish or subscribe to unchanged. Components are compared [where they are used](#references).

Change paths pinpoint the exact location, for example:

```text
asyncapi.channels[payment.completed]
asyncapi.channels[payment.refunded].publish.message.payload.required[charge_id]
asyncapi.operations[sendOrder].action
```

## gRPC

`pacto diff` compares referenced `.proto` files at the service, rpc, message, field and enum level.

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
| `grpc.enums` | Added | NON_BREAKING |
| `grpc.enums` | Removed | **BREAKING** |
| `grpc.enums.values` | Added | POTENTIAL_BREAKING |
| `grpc.enums.values` | Removed / Modified | **BREAKING** |

proto3 has no `required`, so an added rpc, message or field is always wire-compatible with existing clients. An added enum value is `POTENTIAL_BREAKING`: the scan cannot tell whether the enum reaches a reader that switches over every value. A removed or renumbered value is `BREAKING`. Everything else here is `BREAKING`. Removing a service, rpc, message or field breaks every caller. A changed rpc signature (including a switch between unary and streaming), or a field whose type or number changed, breaks the wire format for clients built against the old descriptor. For that reason a modified field is `BREAKING`.

Change paths pinpoint the exact location, for example:

```text
grpc.services[FraudService]
grpc.rpcs[FraudService.EvaluateTransaction]
grpc.messages[EvaluateTransactionRequest].fields[metadata]
```

### Limits of the proto comparison

The comparison scans proto3 source as text. It runs no protobuf compiler, which has these effects:

- **`import`s stay unresolved.** Pacto compares only the declarations in the referenced file. A message that moves into an imported file reads as a removal.
- **Nested messages, nested enums and `oneof` bodies are skipped.** Their fields are left out of the enclosing message, so a change inside one produces no change entry.
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
| `schema.description`, `title`, `examples` and other documentation | Added / Removed / Modified | NON_BREAKING |
| `schema.*` (any other path) | Added / Removed / Modified | POTENTIAL_BREAKING |

A configuration or policy schema carries no position. A configuration schema
constrains a value the platform supplies, so both `required` transitions take
the conservative answer and stay `BREAKING`.

## Schema position

An interface schema is judged by the position it sits in, because the same
edit helps one side of an exchange and hurts the other.

| Position | Where | Who reads it | Widening | Narrowing |
|----------|-------|--------------|----------|-----------|
| request | OpenAPI request bodies and parameters, MCP `inputSchema` | the new provider, from an old caller | NON_BREAKING | **BREAKING** |
| response | OpenAPI responses, MCP `outputSchema` | an old consumer, which may switch over every `enum` value | **BREAKING** | NON_BREAKING |
| record | AsyncAPI channels and operations | old and new code at once, in both directions | **BREAKING** | **BREAKING** |

| Edit | Effect |
|------|--------|
| `enum` value, `oneOf` or `anyOf` variant, or `type` member added | widens |
| `type` changed from `integer` to `number`, or `nullable: true` set | widens |
| `maxLength`, `maximum`, `maxItems` or `maxProperties` raised, or a `min*` bound lowered | widens |
| a bound, `pattern`, `format`, `const`, `enum`, `allOf` or `items` removed | widens |
| `additionalProperties: false` relaxed to a schema or removed | widens |
| `required` entry removed, or `allOf` member removed | widens |
| the mirror of each row above | narrows |
| `pattern`, `format`, `const` or `multipleOf` rewritten, or `type` changed to an unrelated type | breaks in every position |
| anything inside `not` or `if`, a `default`, `readOnly` or `discriminator` changed, any other keyword | breaks in every position |
| a `$ref` Pacto cannot resolve | POTENTIAL_BREAKING in every position |
| an `x-` extension keyword added, removed or changed | POTENTIAL_BREAKING in every position |
| `description`, `title`, `summary`, `example`, `examples`, `$comment`, `externalDocs` or `deprecated` | NON_BREAKING in every position |

Properties have their own rules:

- Adding an optional property is `NON_BREAKING` in every position, because a reader tolerates a field it does not know.
- Removing an optional property is `BREAKING` from a response or a record whether or not `required` listed it. Most payload schemas list nothing, and the field is data a reader may use.
- Removing one from a request is `POTENTIAL_BREAKING`: the provider drops the caller's field unread.
- An object with `additionalProperties: false` accepts no undeclared field, so adding a property widens it and removing one narrows it.
- Inside a response header every grade stops at `POTENTIAL_BREAKING`, because removing the whole header is `POTENTIAL_BREAKING`.

### References and components { #references }

Each `$ref` is resolved first, so a component is judged where it is used:

- A component used in more than one position, such as a schema in a request body and in a response, is judged in each one. Pacto reports one change with the worst grade, at the usage path where that grade applies. The reason names the component and its usages.
- Pacto compares what a ref resolves to. Renaming a component with identical content is no change, and a ref pointed at a different schema is graded by the structural difference.
- Local refs (`#/components/...`, `#/definitions/...`, `#/$defs/...`) and relative file refs inside the bundle resolve, and a ref inside another file resolves against that file. Request bodies, responses, parameters and headers resolve the same way as schemas.
- A remote (`http` or `https`) ref, or one whose target is missing, is `POTENTIAL_BREAKING`, and the reason says Pacto could not resolve it. The same unresolved ref in both revisions reports no change, because Pacto sees nothing change.
- A recursive schema is compared once per position, so cycles terminate.
- A component that no path, channel or operation reaches through a `$ref` is reported as one `NON_BREAKING` change, because no consumer reads it. Security schemes are named without a `$ref`, so that report leaves them out.
- In AsyncAPI 3 a ref from an operation to its channel or message is compared as text, because the channel is compared on its own.

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
