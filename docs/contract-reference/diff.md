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
| `pactoVersion` | Added / Modified / Removed | NON_BREAKING |
| `service.name` | Modified | **BREAKING** |
| `service.version` | Modified | NON_BREAKING |
| `service.owner.team` | Added / Modified / Removed | NON_BREAKING |
| `service.owner.dri` | Added / Modified / Removed | NON_BREAKING |
| `service.owner.contacts[]` | Added / Modified / Removed | NON_BREAKING |
| `service.image` | Added / Modified / Removed | NON_BREAKING |
| `service.chart` | Added / Modified / Removed | NON_BREAKING |

The owner block is compared field by field, so changing a `dri` or adding a
contact while the `team` is unchanged surfaces the specific change rather than an
opaque whole-owner modification. Contacts are keyed by `type:value`; a `purpose`
change on an existing contact is a modification. The `service.image` comparison
includes the `private` flag, so toggling image privacy is reported even when the
`ref` is unchanged.

## Interfaces

| Field | Change | Classification |
|-------|--------|----------------|
| `interfaces` | Added | NON_BREAKING |
| `interfaces` | Removed | **BREAKING** |
| `interfaces.type` | Modified | **BREAKING** |
| `interfaces.port` | Modified | **BREAKING** |
| `interfaces.port` | Added | POTENTIAL_BREAKING |
| `interfaces.port` | Removed | **BREAKING** |
| `interfaces.visibility` | Modified | POTENTIAL_BREAKING |
| `interfaces.contract` | Modified | POTENTIAL_BREAKING |

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

## Runtime

| Field | Change | Classification |
|-------|--------|----------------|
| `runtime.workload` | Modified | **BREAKING** |
| `runtime.state.type` | Modified | **BREAKING** |
| `runtime.state.persistence.scope` | Modified | **BREAKING** |
| `runtime.state.persistence.durability` | Modified | **BREAKING** |
| `runtime.state.dataCriticality` | Modified | POTENTIAL_BREAKING |
| `runtime.lifecycle.upgradeStrategy` | Added | NON_BREAKING |
| `runtime.lifecycle.upgradeStrategy` | Modified | POTENTIAL_BREAKING |
| `runtime.lifecycle.upgradeStrategy` | Removed | POTENTIAL_BREAKING |
| `runtime.lifecycle.gracefulShutdownSeconds` | Added / Modified / Removed | NON_BREAKING |
| `runtime.health.interface` | Added | NON_BREAKING |
| `runtime.health.interface` | Modified | POTENTIAL_BREAKING |
| `runtime.health.interface` | Removed | POTENTIAL_BREAKING |
| `runtime.health.path` | Added | NON_BREAKING |
| `runtime.health.path` | Modified | POTENTIAL_BREAKING |
| `runtime.health.path` | Removed | POTENTIAL_BREAKING |
| `runtime.health.initialDelaySeconds` | Added / Modified / Removed | NON_BREAKING |
| `runtime.metrics.interface` | Added | NON_BREAKING |
| `runtime.metrics.interface` | Modified | POTENTIAL_BREAKING |
| `runtime.metrics.interface` | Removed | POTENTIAL_BREAKING |
| `runtime.metrics.path` | Added | NON_BREAKING |
| `runtime.metrics.path` | Modified | POTENTIAL_BREAKING |
| `runtime.metrics.path` | Removed | POTENTIAL_BREAKING |

Adding a `health` or `metrics` block (or one of its fields) is a new capability
and classifies as `NON_BREAKING`; removing the `interface`/`path` loses a runtime
check and classifies as `POTENTIAL_BREAKING`. `initialDelaySeconds` is always
`NON_BREAKING`. Health and metrics are compared field by field — a whole-block
add or remove surfaces as the corresponding per-field changes.

## Scaling

| Field | Change | Classification |
|-------|--------|----------------|
| `scaling` | Added | NON_BREAKING |
| `scaling` | Removed | POTENTIAL_BREAKING |
| `scaling.replicas` | Modified | POTENTIAL_BREAKING |
| `scaling.min` | Modified | POTENTIAL_BREAKING |
| `scaling.max` | Modified | NON_BREAKING |

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
| `openapi.parameters` | Added | POTENTIAL_BREAKING |
| `openapi.parameters` | Removed | **BREAKING** |
| `openapi.parameters` | Modified | POTENTIAL_BREAKING |

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
| `readiness.checks[]` | Added / Modified / Removed | NON_BREAKING |

All readiness changes are classified as `NON_BREAKING` — readiness tracks operational
maturity and does not affect runtime compatibility. Adding or removing the whole
`readiness` block surfaces as a single `readiness` change; otherwise the gate
fields (`expires`, `minScore`, `partialCredit`) are compared individually. Checks
are keyed by `id`, and a check whose `status`, `weight`, `evidence` or any other
field changed is reported as a modification of that check (e.g.
`readiness.checks[dashboard]`). The `readiness.history` revision log is not diffed:
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
