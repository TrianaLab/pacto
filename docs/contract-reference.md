---
title: Contract Reference
layout: default
nav_order: 4
---

# Contract Reference (v1.0)
{: .no_toc }

A Pacto contract is a YAML file (`pacto.yaml`) that describes a service's operational interface. This page covers every section, field, validation rule, and change classification rule.

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

The canonical JSON Schema is available at [`schema/pacto-v1.0.schema.json`](https://github.com/TrianaLab/pacto/blob/main/schema/pacto-v1.0.schema.json).

---

## Bundle structure

A Pacto bundle is a self-contained directory (or OCI artifact) with the following layout:

```
/
├── pacto.yaml
├── interfaces/
│   ├── openapi.yaml
│   ├── service.proto
│   └── events.yaml
└── configuration/
    └── schema.json
```

All files referenced by `pacto.yaml` must exist within the bundle.

---

## Full example

```yaml
pactoVersion: "1.0"

service:
  name: payments-api
  version: 2.1.0
  owner: team/payments
  image:
    ref: ghcr.io/acme/payments-api:2.1.0
    private: true

interfaces:
  - name: rest-api
    type: http
    port: 8080
    visibility: public
    contract: interfaces/openapi.yaml

  - name: grpc-api
    type: grpc
    port: 9090
    visibility: internal
    contract: interfaces/service.proto

  - name: order-events
    type: event
    visibility: internal
    contract: interfaces/events.yaml

configuration:
  schema: configuration/schema.json

dependencies:
  - ref: ghcr.io/acme/auth-pacto@sha256:abc123def456
    required: true
    compatibility: "^2.0.0"

  - ref: ghcr.io/acme/notifications-pacto:1.0.0
    required: false
    compatibility: "~1.0.0"

runtime:
  workload: service

  state:
    type: stateful
    persistence:
      scope: local
      durability: persistent
    dataCriticality: high

  lifecycle:
    upgradeStrategy: ordered
    gracefulShutdownSeconds: 30

  health:
    interface: rest-api
    path: /health
    initialDelaySeconds: 15

scaling:
  min: 2
  max: 10

metadata:
  team: payments
  tier: critical
```

---

## Sections

### `pactoVersion`

The contract specification version. Currently only `"1.0"` is supported.

```yaml
pactoVersion: "1.0"
```

---

### `service`

Identifies the service.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Pattern: `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` |
| `version` | string | Yes | Valid semver (e.g., `2.1.0`) |
| `owner` | string | No | |
| `image` | [Image](#image) | No | |

#### Image

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `ref` | string | Yes | Non-empty. Valid OCI image reference |
| `private` | boolean | No | |

---

### `interfaces`

Declares the service's communication boundaries. **At least one interface is required.**

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty. Must be unique across interfaces |
| `type` | string | Yes | Enum: `http`, `grpc`, `event` |
| `port` | integer | Conditional | Range: 1-65535. Required for `http` and `grpc` |
| `visibility` | string | No | Enum: `public`, `internal`. Default: `internal` |
| `contract` | string | Conditional | Non-empty. Required for `grpc` and `event` |

#### Conditional requirements

| Interface type | Required fields |
|---|---|
| `http` | `port` |
| `grpc` | `port`, `contract` |
| `event` | `contract` |

{: .note }
Interface names must be unique within a contract. The `contract` field for `http` interfaces is optional but recommended (typically an OpenAPI spec).

---

### `configuration`

Defines the service's configuration model.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `schema` | string | Yes | Non-empty. Must reference a file in the bundle |

Required configuration keys are derived from the JSON Schema's `required` array.

---

### `dependencies`

Declares dependencies on other services via their Pacto contracts.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `ref` | string | Yes | Non-empty. Valid OCI reference |
| `required` | boolean | No | Default: `false` |
| `compatibility` | string | Yes | Non-empty. Valid semver constraint |

{: .tip }
Use digest-pinned references (`@sha256:...`) for production contracts. Tag-based references produce a validation warning.

---

### `runtime`

Describes how the service behaves at runtime. This is the most important section for platform engineers.

| Field | Type | Required |
|-------|------|----------|
| `workload` | string | Yes |
| `state` | [State](#state) | Yes |
| `lifecycle` | [Lifecycle](#lifecycle) | No |
| `health` | [Health](#health) | Yes |

#### `runtime.workload`

A plain string describing the workload type. Enum: `service`, `job`, `scheduled`.

| Value | Description |
|-------|-------------|
| `service` | Long-running process |
| `job` | Runs to completion |
| `scheduled` | Runs on a schedule |

#### `runtime.state`

| Field | Type | Required | Enum values |
|-------|------|----------|-------------|
| `type` | string | Yes | `stateless`, `stateful`, `hybrid` |
| `persistence` | [Persistence](#persistence) | Yes | |
| `dataCriticality` | string | Yes | `low`, `medium`, `high` |

##### Persistence

| Field | Type | Required | Enum values |
|-------|------|----------|-------------|
| `scope` | string | Yes | `local`, `shared` |
| `durability` | string | Yes | `ephemeral`, `persistent` |

##### State invariants

| Condition | Constraint |
|---|---|
| `type: stateless` | `durability` must be `ephemeral` |
| `durability: persistent` | `type` must be `stateful` or `hybrid` |

These invariants are enforced by both the JSON Schema and cross-field validation.

#### `runtime.lifecycle`

Optional. Describes upgrade and shutdown behavior.

| Field | Type | Required | Enum values / Constraints |
|-------|------|----------|-------------|
| `upgradeStrategy` | string | No | `rolling`, `recreate`, `ordered` |
| `gracefulShutdownSeconds` | integer | No | Minimum: 0 |

#### `runtime.health`

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `interface` | string | Yes | Must reference a declared `http` or `grpc` interface |
| `path` | string | Conditional | Required when health interface is `http` |
| `initialDelaySeconds` | integer | No | Minimum: 0 |

---

### `scaling`

Optional. Defines replica bounds.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `min` | integer | Yes | Minimum: 0 |
| `max` | integer | Yes | Minimum: 0. Must be >= `min` |

{: .warning }
Scaling must not be applied to `job` workloads.

---

### `metadata`

Optional. Free-form key-value pairs for organizational use. Not validated beyond type.

```yaml
metadata:
  team: payments
  tier: critical
  on-call: "#payments-oncall"
```

`additionalProperties: false` — no extra fields allowed at any level (except inside `metadata`).

---

## Validation layers

Pacto validates contracts through three successive layers. Each layer short-circuits — if it fails, subsequent layers are skipped.

### Layer 1: Structural (JSON Schema)

Validates against the embedded JSON Schema:
- Field types match
- Required fields are present
- Enum values are valid
- Conditional requirements are met (`http` needs `port`, etc.)
- State invariants are enforced (`stateless` needs `ephemeral`)

### Layer 2: Cross-field

Validates semantic references and consistency:

| Rule | Code |
|---|---|
| `service.version` is valid semver | `INVALID_SEMVER` |
| Interface names are unique | `DUPLICATE_INTERFACE_NAME` |
| `http`/`grpc` interfaces have `port` | `PORT_REQUIRED` |
| `grpc`/`event` interfaces have `contract` | `CONTRACT_REQUIRED` |
| `health.interface` matches a declared interface | `HEALTH_INTERFACE_NOT_FOUND` |
| Health interface is not `event` type | `HEALTH_INTERFACE_INVALID` |
| `health.path` required for `http` health interface | `HEALTH_PATH_REQUIRED` |
| Referenced files exist in the bundle | `FILE_NOT_FOUND` |
| Dependency refs are valid OCI references | `INVALID_OCI_REF` |
| Compatibility ranges are valid semver constraints | `INVALID_COMPATIBILITY` |
| `image.ref` is a valid OCI reference | `INVALID_IMAGE_REF` |
| `scaling.min` <= `scaling.max` | `SCALING_MIN_EXCEEDS_MAX` |
| Job workloads cannot have scaling | `JOB_SCALING_NOT_ALLOWED` |
| Stateless + persistent is invalid | `STATELESS_PERSISTENT_CONFLICT` |

### Layer 3: Semantic

Validates cross-concern consistency:

| Rule | Type |
|---|---|
| `ordered` upgrade strategy with `stateless` state | Warning |

---

## Change classification rules

`pacto diff` classifies every detected change using a deterministic rule table.

### Service identity

| Field | Change | Classification |
|-------|--------|----------------|
| `service.name` | Modified | **BREAKING** |
| `service.version` | Modified | NON_BREAKING |
| `service.owner` | Added / Modified / Removed | NON_BREAKING |
| `service.image` | Added / Modified / Removed | NON_BREAKING |

### Interfaces

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

### Configuration

| Field | Change | Classification |
|-------|--------|----------------|
| `configuration` | Added | NON_BREAKING |
| `configuration` | Removed | **BREAKING** |
| `configuration.schema` | Added | NON_BREAKING |
| `configuration.schema` | Modified | POTENTIAL_BREAKING |
| `configuration.schema` | Removed | **BREAKING** |

### Runtime

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
| `runtime.lifecycle.gracefulShutdownSeconds` | Modified | NON_BREAKING |
| `runtime.health.interface` | Modified | POTENTIAL_BREAKING |
| `runtime.health.path` | Modified | POTENTIAL_BREAKING |
| `runtime.health.initialDelaySeconds` | Modified | NON_BREAKING |

### Scaling

| Field | Change | Classification |
|-------|--------|----------------|
| `scaling` | Added | NON_BREAKING |
| `scaling` | Removed | POTENTIAL_BREAKING |
| `scaling.min` | Modified | POTENTIAL_BREAKING |
| `scaling.max` | Modified | NON_BREAKING |

### Dependencies

| Field | Change | Classification |
|-------|--------|----------------|
| `dependencies` | Added | NON_BREAKING |
| `dependencies` | Removed | **BREAKING** |
| `dependencies.compatibility` | Modified | POTENTIAL_BREAKING |
| `dependencies.required` | Modified | POTENTIAL_BREAKING |

### OpenAPI paths

| Field | Change | Classification |
|-------|--------|----------------|
| `openapi.paths` | Added | NON_BREAKING |
| `openapi.paths` | Removed | **BREAKING** |

### JSON Schema properties

| Field | Change | Classification |
|-------|--------|----------------|
| `schema.properties` | Added | NON_BREAKING |
| `schema.properties` | Removed | **BREAKING** |

Unknown changes default to **POTENTIAL_BREAKING**.
