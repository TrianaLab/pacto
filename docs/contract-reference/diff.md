# Change classification rules

`pacto diff` classifies every detected change using a deterministic rule table. This is what powers breaking change detection in CI pipelines.

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

Interfaces are keyed by `name`. The `type` enum is `openapi`/`asyncapi`/`grpc`; there is no `port` field in v2, and the spec pointer is `ref` (not `contract`).

## Configurations

| Field | Change | Classification |
|-------|--------|----------------|
| `configurations[]` | Added | NON_BREAKING |
| `configurations[]` | Removed | **BREAKING** |
| `configurations[].schema` | Modified | POTENTIAL_BREAKING |
| `configurations[].ref` | Added | NON_BREAKING |
| `configurations[].ref` | Modified | POTENTIAL_BREAKING |
| `configurations[].ref` | Removed | **BREAKING** |
| `configurations[].values.*` | Added / Modified / Removed | NON_BREAKING |

Inline configuration `values` are the provider's own defaults, not part of the
consumer-facing contract surface, so value changes are diffed key by key (e.g.
`configurations[app].values.replicas`) and classified `NON_BREAKING`.

## Policy

| Field | Change | Classification |
|-------|--------|----------------|
| `policies[]` | Added | NON_BREAKING |
| `policies[]` | Removed | POTENTIAL_BREAKING |
| `policies[].schema` | Modified | POTENTIAL_BREAKING |
| `policies[].ref` | Added | NON_BREAKING |
| `policies[].ref` | Modified | POTENTIAL_BREAKING |
| `policies[].ref` | Removed | POTENTIAL_BREAKING |

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

`workload` and `state` are top-level sections in v2. There is no `runtime`,
`lifecycle` or `scaling` section to diff — those fields were removed.

## Capabilities

| Field | Change | Classification |
|-------|--------|----------------|
| `capabilities` | Added | NON_BREAKING |
| `capabilities` | Removed | POTENTIAL_BREAKING |

Capabilities (`health`, `metrics`, `extension`) are compared as a set. Adding a
capability is a new signal (`NON_BREAKING`); removing one loses an observability
guarantee (`POTENTIAL_BREAKING`).

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
| `openapi.request-body` | Modified | POTENTIAL_BREAKING |

### Responses

| Field | Change | Classification |
|-------|--------|----------------|
| `openapi.responses` | Added | NON_BREAKING |
| `openapi.responses` | Removed | **BREAKING** |
| `openapi.responses` | Modified | POTENTIAL_BREAKING |

Change paths use a hierarchical format that pinpoints the exact location, for example:

```
openapi.paths[/users].methods[GET].parameters[filter:query]
openapi.paths[/users].methods[POST].request-body
openapi.paths[/users].methods[GET].responses[200]
```

## JSON Schema (configuration & policy schemas)

Schema files referenced by `configurations[].schema`, `policies[].schema`, or the auto-detected `policy/schema.json` are compared recursively. Every structural difference — properties, types, constraints, defaults, enums, etc. — is detected and classified.

| Field | Change | Classification |
|-------|--------|----------------|
| `schema.properties.*` | Added | POTENTIAL_BREAKING |
| `schema.properties.*` | Removed | POTENTIAL_BREAKING |
| `schema.properties.*` | Modified | POTENTIAL_BREAKING |
| `schema.required` | Added / Removed | **BREAKING** |
| `schema.*` (any other path) | Added / Removed / Modified | POTENTIAL_BREAKING |

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

If you rely on metadata changes being flagged in CI, gate on them separately
(for example with a policy or a custom check).

## SBOM

SBOM changes are reported separately from contract changes. They are **informational only** and never affect the overall diff classification.

| Change | Description |
|--------|-------------|
| Package added | A new package appears in the SBOM |
| Package removed | A package no longer appears in the SBOM |
| Package version modified | A package's version changed |
| Package license modified | A package's license changed |
| Package supplier modified | A package's supplier changed |
