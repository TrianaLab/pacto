# Contract sections

## `pactoVersion`

The contract specification version. Supported values are `"1.0"`, `"1.1"` and `"1.2"`.

```yaml
pactoVersion: "1.2"
```

Each version is validated against its own JSON Schema, selected by the declared
`pactoVersion`. An unrecognized version is a hard error (`UNSUPPORTED_PACTO_VERSION`).

| Version | Adds |
|---------|------|
| `1.0`   | The base contract (service, interfaces, configurations, policies, dependencies, runtime, scaling, metadata). Object-only `owner` (string form removed). |
| `1.1`   | Object-only `owner` inherited from `1.0`. Everything from `1.0` remains valid. No readiness. |
| `1.2`   | Redesigned [`readiness`](#readiness) — per-check `status` + `category`, assessment-level `expires`, `partialCredit`, `history[]`. Object-only `owner` inherited from `1.0`. Everything from `1.0` and `1.1` remains valid. |

Existing `1.0` and `1.1` contracts continue to validate unchanged. The `readiness` section
is only accepted under `pactoVersion: "1.2"`; declaring it under `1.0` or `1.1` is rejected.

---

## `service`

Identifies the service.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Pattern: `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` |
| `version` | string | Yes | Valid semver (e.g., `2.1.0`) |
| `owner` | [OwnerInfo](#ownerinfo) | No | Object only (string form removed) |
| `image` | [Image](#image) | No | |
| `chart` | [Chart](#chart) | No | |

### Image

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `ref` | string | Yes | Non-empty. Valid OCI image reference |
| `private` | boolean | No | |

### Chart

Optional Helm chart reference for deploying the service.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `ref` | string | Yes | Non-empty. Local path (e.g. `./charts/my-chart`) or OCI reference (e.g. `oci://ghcr.io/org/chart`) |
| `version` | string | Yes | Non-empty. Valid semver |

!!! warning
    Local chart references are only allowed during development. `pacto push` rejects contracts with local chart references — use an OCI reference before publishing.

### OwnerInfo

Structured ownership metadata. All fields are optional but at least one must be present.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `team` | string | No | Team name |
| `dri` | string | No | Directly Responsible Individual |
| `contacts` | [OwnerContact](#ownercontact)[] | No | Contact points |

### OwnerContact

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `type` | string | Yes | One of: `email`, `url`, `group`, `oncall`, `chat`, `other` |
| `value` | string | Yes | Non-empty |
| `purpose` | string | No | One of: `ownership`, `support`, `escalation`, `oncall`, `notifications` |

**Examples:**

```yaml
# Structured form (object only — string form removed)
service:
  owner:
    team: foundations
    dri: eduardo.diaz
    contacts:
      - type: email
        value: foundations@acme.com
        purpose: ownership
      - type: chat
        value: "#foundations"

# Minimal object form (at least one field required)
service:
  owner:
    team: payments
```

**Dashboard integration:**

The dashboard uses a canonical owner key for aggregation and navigation:
1. If owner has `team` → uses `team`
2. If owner has `dri` (no team) → uses `dri`

This key is used consistently across the owners aggregation view (`#/owners`), owner detail view (`#/owners/:key`), service list filtering and dependency graph highlighting. See [Platform engineers — dashboard](../platform-engineers.md) for how the dashboard renders these views.

---

## `interfaces`

Declares the service's communication boundaries. Optional — a service with no network interfaces (e.g. a batch job or shared library) may omit this section entirely. The `contract` field points at the interface definition you already publish — an OpenAPI spec, a `.proto` or an event schema — so Pacto references your existing interface rather than redefining it.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty. Must be unique across interfaces |
| `type` | string | Yes | Enum: `http`, `grpc`, `event` |
| `port` | integer | Conditional | Range: 1-65535. Required for `http` and `grpc` |
| `visibility` | string | No | Enum: `public`, `internal`. Default: `internal` |
| `contract` | string | Conditional | Non-empty. Required for `grpc` and `event` |

### Conditional requirements

| Interface type | Required fields |
|---|---|
| `http` | `port` |
| `grpc` | `port`, `contract` |
| `event` | `contract` |

!!! note
    Interface names must be unique within a contract. The `contract` field for `http` interfaces is optional but recommended (typically an OpenAPI spec).

---

## `configurations`

Defines the service's configuration model. Optional — services with no configuration schema may omit this section entirely.

The `configurations` section is an array of named configuration entries:

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the configuration entry |
| `schema` | string | Conditional | Non-empty. Must reference a file in the bundle. Required if `ref` is not set |
| `ref` | string | Conditional | Non-empty. OCI or local reference to another Pacto contract. Required if `schema` is not set |
| `values` | object | No | Must conform to the schema defined in `schema` |

When `schema` is used, the configuration schema is a local file within the bundle. When `ref` is used, the schema is resolved from another Pacto contract's bundle at the fixed path `configuration/schema.json`. **`schema` and `ref` are mutually exclusive** — each entry must use one or the other, not both. When `ref` is set, `schema` and `values` must not be present.

Required configuration keys are derived from the JSON Schema's `required` array.

The optional `values` field provides default configuration values that are validated against the local `schema`. A `ref:` entry carries no `values` — the two are mutually exclusive (see above); to attach values, vendor a local `schema:`. Values are useful for documenting expected defaults or providing environment-specific overrides via the `--set` and `--values` flags (see [Contract overrides](overrides.md#contract-overrides)).

!!! tip
    All files referenced by the contract — including the configuration schema — are packaged into the bundle when you run `pacto push`. The bundle is a self-contained OCI artifact that includes `pacto.yaml`, interface contracts, the configuration schema, and any other files in the contract directory.

### External configuration schema reference

Instead of vendoring a configuration schema into the bundle, you can reference another Pacto contract that contains it. The referenced contract's bundle must have the schema at the fixed path `configuration/schema.json`:

```yaml
configurations:
  - name: platform
    ref: oci://ghcr.io/acme/platform-config-pacto:1.0.0
```

This enables centralized configuration management — a platform team publishes a single configuration contract, and all services reference it. The reference supports recursive resolution: if the referenced contract itself has a `configurations[].ref`, Pacto follows the chain (with cycle detection) using the same OCI resolution and caching infrastructure as dependencies.

!!! tip
    Configuration references create **reference edges** in the dependency graph, distinct from `dependencies[].ref` edges. Use `pacto graph --with-references` to visualize them, or `pacto graph --only-references` to show only reference edges. In the dashboard graph, reference edges appear as dashed lines.

!!! warning
    Local configuration references (`file://` and bare paths) are only allowed during development. `pacto push` rejects contracts with local configuration refs — all refs must use `oci://` before publishing.

!!! tip
    Configuration references are pinned in `pacto.lock` alongside dependencies. The full transitive reference closure (N-hop config/policy jumps) is resolved and verified. See [Lockfile](../lockfile.md).

### Secret references

Secrets should never be stored as literal values in a contract. Instead, use a reference convention that the platform resolves at deployment time. The contract declares *what* the service needs; the platform decides *how* to provide it.

```yaml
configurations:
  - name: default
    schema: configuration/schema.json
    values:
        DB_HOST: prod-db.internal
        DB_PORT: 5432
        DB_PASSWORD: secret://vault/payments/db-password
        API_KEY: secret://vault/payments/stripe-api-key
```

The `secret://` prefix is a convention — Pacto treats it as an opaque string value. Your platform tooling (Kubernetes operators, Terraform modules, deployment scripts) interprets these references and injects the actual secret at runtime. This keeps sensitive values out of the contract while making the dependency on secrets explicit and auditable.

Your configuration JSON Schema should declare secret fields as strings:

```json
{
  "type": "object",
  "properties": {
    "DB_PASSWORD": { "type": "string", "description": "Database password (secret reference)" },
    "API_KEY": { "type": "string", "description": "Stripe API key (secret reference)" }
  },
  "required": ["DB_PASSWORD", "API_KEY"]
}
```

---

## Configuration Schema Ownership Models

The configurations schema field is an **interface**. It defines a boundary between a service and its environment. The schema itself is always a JSON Schema document — but its *meaning* depends on who defines it. Because it is a plain JSON Schema, this is usually a schema you already have rather than one written for Pacto — a service's configuration interface is often its Helm chart's `values.schema.json`, vendored into the bundle (see below). Compose the interface you already have; Pacto is deliberately not another configuration language.

### Service-Defined Schema

When a service defines its own configuration schema, the schema expresses **what the service requires** to run. The service author knows what configuration keys the service reads, what types they expect, and which ones are mandatory.

```yaml
configurations:
  - name: default
    schema: configuration/schema.json
```

The contract carries its own requirements, so each team defines exactly what its service needs and the bundle deploys on any platform. See [Composition patterns](../patterns.md) for when to choose this model.

### Platform-Defined Schema

When a platform team defines a shared configuration schema, the schema expresses **what the platform provides**. It describes the platform's capabilities — databases, caches, observability endpoints, feature flags — as a structured contract.

There are two approaches for distributing platform schemas:

**Vendored (local path):** The platform team publishes the schema externally, and services copy it into their bundle at build time:

```yaml
configurations:
  - name: platform
    schema: configuration/platform-schema.json
```

**Referenced (OCI):** Services reference the platform's configuration contract directly via `configurations[].ref`. No vendoring required — Pacto resolves the schema from the referenced bundle at the fixed path `configuration/schema.json`:

```yaml
configurations:
  - name: platform
    ref: oci://ghcr.io/acme/platform-config-pacto:1.0.0
```

All services share a common configuration vocabulary the platform team controls and validates centrally — whether referenced via OCI or vendored locally. See [Platform engineers](../platform-engineers.md) and [Composition patterns](../patterns.md) for the platform-as-a-product recipe.

!!! info
    In Pacto, the configuration schema is an **interface**. Depending on ownership, it describes either:

    - what a **service requires** (service-defined), or
    - what a **platform provides** (platform-defined)

    The schema format and validation mechanics are identical in both cases. The difference is purely one of ownership and intent.

### Hybrid Approaches

In practice, organizations may combine both models — a platform-defined base schema that covers shared infrastructure (database connections, observability, secrets) with service-specific extensions for application-level configuration. Pacto does not prescribe a specific pattern; the schema format is identical regardless of where it originates. Since `configurations` is an array, you can have multiple named entries — one referencing a platform schema and another defining a service-specific schema. Note that `schema` and `ref` are mutually exclusive within a single entry — choose one approach per entry.

---

## `policies`

Defines or references policy constraints for the contract. Optional — services not subject to a policy may omit this section entirely. A policy is a JSON Schema that validates the contract itself, enabling platform teams to enforce organizational standards (e.g., require health endpoints, mandate specific ports, enforce visibility rules).

When present, each entry must have a `name` and either `schema` or `ref` specified.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the policy entry |
| `schema` | string | Conditional | Non-empty. Path to a JSON Schema file in the bundle (convention: `policy/schema.json`). Required if `ref` is not set |
| `ref` | string | Conditional | Non-empty. OCI or local reference to another Pacto contract. If the referenced contract declares `policies[]`, those schemas are used directly; otherwise falls back to the fixed path `policy/schema.json`. Required if `schema` is not set |

**`schema` and `ref` are mutually exclusive** — a contract either defines its own policy inline or references an external one, not both.

### Policy as a contract author

To define a policy, create a JSON Schema that describes constraints on `pacto.yaml` contracts and place it at `policy/schema.json` in the bundle:

```yaml
# pacto.yaml — a policy contract
pactoVersion: "1.0"
service:
  name: platform-policy
  version: 1.0.0
  owner:
    team: platform
policies:
  - name: platform-policy
    schema: policy/schema.json
```

Example policy schema (`policy/schema.json`) requiring all contracts to have a health check:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["runtime"],
  "properties": {
    "runtime": {
      "type": "object",
      "required": ["health"],
      "properties": {
        "health": {
          "type": "object",
          "required": ["interface", "path"]
        }
      }
    }
  }
}
```

### Policy as a contract consumer

To adopt a policy, reference the policy contract via OCI:

```yaml
policies:
  - name: platform-policy
    ref: oci://ghcr.io/acme/platform-policy-pacto:1.0.0
```

When a consumer references a policy contract, Pacto uses conditional resolution: if the referenced contract explicitly declares `policies[]` entries, those schemas are used directly (supporting custom paths and multiple schemas). If the referenced contract has no `policies[]` entries, Pacto falls back to reading the fixed path `policy/schema.json`. The reference supports recursive resolution: if the referenced contract itself has a `policies[].ref`, Pacto follows the chain (with cycle detection) using the same OCI resolution and caching infrastructure as dependencies.

!!! tip
    Like `configurations[].ref`, policy references create **reference edges** in the dependency graph. Use `pacto graph --with-references` to see them alongside dependencies.

!!! warning
    Local policy references (`file://` and bare paths) are only allowed during development. `pacto push` rejects contracts with local `policies[].ref` — all refs must use `oci://` before publishing.

!!! info
    `pacto push` resolves and enforces all remote `policies[].ref` entries before publishing. If the contract violates any referenced policy schema, the push is rejected. This ensures non-compliant contracts are never published to the registry.

!!! tip
    Policy references are pinned in `pacto.lock` alongside dependencies and config references. The full transitive reference closure is resolved and verified. See [Lockfile](../lockfile.md).

### Bundle structure with policy

```
/
├── pacto.yaml
├── interfaces/              ← optional
├── configuration/           ← optional
│   └── schema.json
├── policy/                  ← optional
│   └── schema.json          ← policy schema (for policy authors)
├── docs/                    ← optional
└── sbom/                    ← optional
```

---

## `dependencies`

Declares dependencies on other services via their Pacto contracts.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the dependency |
| `ref` | string | Yes | Non-empty. OCI reference (`oci://...`) or local path (`file://...` or bare path) |
| `required` | boolean | No | Default: `false` |
| `compatibility` | string | Yes | Non-empty. Valid semver constraint |

!!! note
    `required` is informational. Pacto itself does not act on it — it is metadata for downstream consumers (the dashboard uses it to compute blast radius; deployment tooling may use it to gate rollout). Treat it as a declaration of *intent*, not a deployment-time guard.

### Dependency reference schemes

| Scheme | Example | Description |
|--------|---------|-------------|
| `oci://` | `oci://ghcr.io/acme/auth-pacto:1.0.0` | OCI registry reference (required for `pacto push`) |
| `oci://` (no tag) | `oci://ghcr.io/acme/auth-pacto` | Resolved to the highest semver tag satisfying `compatibility` |
| `file://` | `file://../shared-db` | Local filesystem path |
| *(bare path)* | `../shared-db` | Local filesystem path (shorthand for `file://`) |

When an `oci://` reference omits the tag, pacto queries the registry for available tags and selects the highest semver version that satisfies the `compatibility` constraint. For example, with `compatibility: "^2.0.0"` and available tags `1.0.0`, `2.0.0`, `2.3.0`, `3.0.0`, pacto resolves to `2.3.0`. Tag listings are cached in memory for the duration of the command, so multiple dependencies pointing to the same repository only trigger a single registry query.

!!! note
    Validation rejects OCI references whose tag or digest is malformed. Tags must follow the OCI tag grammar (`[A-Za-z0-9_][A-Za-z0-9._-]{0,127}`), and digests must be well-formed (`sha256:<64 hex>` or `sha512:<128 hex>`). The same check applies to `service.image.ref`, `service.chart.ref`, and config/policy refs.

### Compatibility constraint examples

Pacto uses [Masterminds/semver](https://github.com/Masterminds/semver#checking-version-constraints) constraint syntax:

| Constraint | Matches | Use case |
|------------|---------|----------|
| `^2.0.0` | `>= 2.0.0`, `< 3.0.0` | Accept patches and minors within a major version |
| `~2.1.0` | `>= 2.1.0`, `< 2.2.0` | Accept only patches within a minor version |
| `>= 2.0.0` | `2.0.0` and above (including `3.x`, `4.x`, …) | Track the latest version above a floor |
| `>= 2.0.0, < 4.0.0` | `2.x` and `3.x` only | Constrain to a range of major versions |
| `*` | Any version | Always resolve to the absolute latest |

!!! warning
    Local dependency references (`file://` and bare paths) are only allowed during development. `pacto push` rejects contracts with local dependencies — all refs must use `oci://` before publishing.

!!! tip
    Use digest-pinned references (`oci://...@sha256:...`) for production contracts. Tag-based references produce a validation warning.

!!! tip
    If your service depends on a cloud-managed resource (e.g. GCP Cloud SQL, AWS SNS, Azure Service Bus), create a lightweight Pacto contract representing that resource and reference it as a dependency. This makes cloud dependencies explicit and version-tracked alongside your service contracts.

!!! tip
    Use `pacto lock` to pin the full transitive dependency closure to exact digests. When a `pacto.lock` file is present, every resolved dependency must match the pinned digest or the command fails. See [Lockfile](../lockfile.md) for details.

---

## `runtime`

Describes how the service behaves at runtime. This section is what lets platforms make informed deployment decisions without guessing. Optional — a minimal contract (e.g. a lightweight dependency declaration) may omit it entirely.

| Field | Type | Required |
|-------|------|----------|
| `workload` | string | Yes |
| `state` | [State](#runtimestate) | Yes |
| `lifecycle` | [Lifecycle](#runtimelifecycle) | No |
| `health` | [Health](#runtimehealth) | No |
| `metrics` | [Metrics](#runtimemetrics) | No |

### `runtime.workload`

A plain string describing the workload type. Enum: `service`, `job`, `scheduled`.

| Value | Description |
|-------|-------------|
| `service` | A long-running process that serves requests continuously |
| `job` | A one-shot task that runs to completion and then exits |
| `scheduled` | A task that runs on a recurring schedule (e.g. cron) |

### `runtime.state`

Instead of platforms guessing whether a service needs persistent storage, stable network identity or special upgrade procedures, the contract declares it explicitly.

| Field | Type | Required | Enum values |
|-------|------|----------|-------------|
| `type` | string | Yes | `stateless`, `stateful`, `hybrid` |
| `persistence` | [Persistence](#persistence) | Yes | |
| `dataCriticality` | string | Yes | `low`, `medium`, `high` |

**State types:**

| Value | What it means | Example services |
|-------|---------------|------------------|
| `stateless` | No data retained between requests. Any instance can handle any request. Instances are interchangeable. | REST APIs, reverse proxies, API gateways |
| `stateful` | Retains data between requests. Requires stable storage or instance affinity. | Databases, message brokers, distributed caches |
| `hybrid` | Handles requests statelessly but keeps selective in-memory or local state that enriches behavior. Loss of that state degrades but doesn't break the service. | APIs with local caches, services with in-memory session stores |

**How platforms interpret state:**

The combination of `state.type`, `persistence.scope` and `persistence.durability` tells a platform exactly what infrastructure a service needs — these are platform-agnostic signals, not Kubernetes prescriptions. See [Platform engineers](../platform-engineers.md) for the full contract-field → platform-decision mapping (Deployment/StatefulSet/PVC and the equivalents on Nomad, ECS or a custom platform).

**Data criticality:**

| Value | What it means |
|-------|---------------|
| `low` | Loss of data has minimal impact. Can be regenerated or is non-essential. |
| `medium` | Loss has moderate impact. May require manual recovery. |
| `high` | Loss has severe business impact. Must be prevented. Implies backups, replication, stricter disruption budgets. |

#### Persistence

| Field | Type | Required | Enum values |
|-------|------|----------|-------------|
| `scope` | string | Yes | `local`, `shared` |
| `durability` | string | Yes | `ephemeral`, `persistent` |

- **`local`** — data is confined to a single instance. Not shared across replicas.
- **`shared`** — data is shared across all instances via a common store.
- **`ephemeral`** — data can be lost on restart without impact. Caches, temp files, reconstructible state.
- **`persistent`** — data must survive restarts. Requires durable storage.

#### State invariants

| Condition | Constraint |
|---|---|
| `type: stateless` | `durability` must be `ephemeral` |
| `durability: persistent` | `type` must be `stateful` or `hybrid` |

These invariants are enforced by both the JSON Schema and cross-field validation. A stateless service with persistent storage is a contradiction — validation catches it.

### `runtime.lifecycle`

Optional. Describes upgrade and shutdown behavior.

| Field | Type | Required | Enum values / Constraints |
|-------|------|----------|-------------|
| `upgradeStrategy` | string | No | `rolling`, `recreate`, `ordered` |
| `gracefulShutdownSeconds` | integer | No | Minimum: 0 |

### `runtime.health`

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `interface` | string | Yes | Must reference a declared `http` or `grpc` interface |
| `path` | string | Conditional | Required when health interface is `http` |
| `initialDelaySeconds` | integer | No | Minimum: 0 |

### `runtime.metrics`

Optional. Tells the platform where to scrape metrics from the service (e.g. Prometheus endpoint).

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `interface` | string | Yes | Must reference a declared `http` or `grpc` interface |
| `path` | string | Conditional | Required when metrics interface is `http` |

```yaml
runtime:
  metrics:
    interface: api
    path: /metrics
```

---

## `scaling`

Optional. Defines replica count as either an exact number or a min/max range. Uses one of two mutually exclusive forms.

### Fixed replica count

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `replicas` | integer | Yes | Minimum: 0. Mutually exclusive with `min`/`max` |

```yaml
scaling:
  replicas: 3
```

### Auto-scaling range

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `min` | integer | Yes | Minimum: 0. Mutually exclusive with `replicas` |
| `max` | integer | Yes | Minimum: 0. Must be >= `min`. Mutually exclusive with `replicas` |

```yaml
scaling:
  min: 2
  max: 10
```

**How the two forms relate.** `replicas` is shorthand: at parse time `min` and
`max` are also set to `replicas` so state-based tooling (operator, dashboard)
reads a `min`/`max` range. `pacto diff` still distinguishes the forms: two
replicas-form contracts report a `scaling.replicas` change; switching between
forms reports a whole-`scaling` change.

!!! warning
    Scaling must not be applied to `job` workloads.

!!! note "`0` is not scale-to-zero"
    The JSON Schema declares `minimum: 0` for `min`/`max`/`replicas`, but `min`
    and `max` are plain (non-pointer) integers internally, so an explicit `0` is
    indistinguishable from an omitted value. The operator and dashboard treat
    `0` bounds as "unset" and drop them — Pacto does not model scale-to-zero.
    Use `min: 1` if you need an explicit lower bound to be reported.

---

## `readiness`

Optional. **Requires `pactoVersion: "1.2"`.** Declares operational readiness
state for the service in a provider-neutral way. Each check has a completion status,
optional category and weight. The assessment includes an expiry date and scoring
configuration. Pacto computes a readiness score from check statuses and weights.

```yaml
readiness:
  expires: "2027-06-30"  # assessment-level expiry (YYYY-MM-DD)
  minScore: 80           # gate: the derived score must be >= this (omitted ⇒ 100)
  partialCredit: 0.5     # weight multiplier for partial checks (omitted ⇒ 0.5)
  
  checks:
    - id: dashboard
      type: url
      status: done
      category: observability
      weight: 20
      evidence: https://grafana.example.com/d/service-dashboard
      description: Main production dashboard

    - id: runbook
      type: document
      status: partial
      category: documentation
      weight: 15
      evidence: docs/runbook.md
      description: Incident runbook exists but missing escalation procedures

    - id: security-review
      type: ticket
      status: not-done
      category: security
      weight: 25
      evidence: JIRA-1234
      description: Security assessment pending

    - id: legacy-cleanup
      type: ticket
      status: deferred
      category: code-quality
      weight: 10
      evidence: TECH-5678
      description: Post-launch cleanup task

  history:
    - date: "2026-12-15"
      version: 1.0.0
      author: alice
      description: Initial readiness assessment
```

`readiness.expires`, `readiness.checks[]` and per-check `status` are required when `readiness`
is present. `readiness.minScore`, `readiness.partialCredit` and `readiness.history[]` are optional.

**`readiness` fields:**

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `expires` | string | Yes | Assessment-level expiry as a strict `YYYY-MM-DD` date. If the current date is past this date, ALL checks earn 0 weight regardless of status. Parsing is exact round-trip: zero-padded fields required and the value must re-serialize unchanged, so `2026-1-1`, RFC 3339 timestamps and impossible dates (e.g. `2026-02-30`) are rejected. |
| `minScore` | integer | No | Gate threshold on the same 0–100 scale as the score. Omitted ⇒ `100` (full compliance required). Enforced by `pacto validate --readiness` and the operator. |
| `partialCredit` | number | No | Multiplier for `partial` status checks (0.0–1.0). Omitted ⇒ `0.5` (half credit). |
| `checks` | [Check](#check-fields)[] | Yes | At least one check. |
| `history` | [HistoryEntry](#history-entry)[] | No | Audit trail of readiness assessment changes. |

### Check fields

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `id` | string | Yes | Stable readiness requirement id. Pattern: `^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`. Unique within the contract. Policies usually target this field. |
| `type` | string | Yes | Enum: `url`, `document`, `ticket`, `report`, `artifact`, `identifier`, `other`. Evidence type. |
| `status` | string | Yes | Enum: `done`, `partial`, `not-done`, `deferred`. Completion state for the check. |
| `category` | string | No | Enum: `architecture`, `testing`, `code-quality`, `observability`, `security`, `documentation`, `infrastructure`, `ci-cd`, `deployment`, `resilience`, `backup-recovery`, `incident-response`, `compliance`, `other`. Categorizes the requirement type. |
| `weight` | integer | Yes | Contribution to the readiness score. Range `0`–`100`. |
| `evidence` | string | Yes | Evidence location (URL, file path, ticket ID, etc.). Non-empty. |
| `description` | string | No | Optional human-readable explanation (non-blank when present). |

### History entry

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `date` | string | Yes | Date of the change as `YYYY-MM-DD`. |
| `version` | string | Yes | Contract version at the time of the change. Non-blank. |
| `author` | string | Yes | Person or system that made the change. Non-blank. |
| `description` | string | Yes | Description of what changed. |

The service owner is declared at the contract level, so readiness checks carry no
per-check owner. The readiness score is **not** stored in the contract — it is
computed by tooling (`pacto explain`, the dashboard and the operator) from check
statuses, weights, the assessment expiry and the current time.

### Readiness score and gate

```text
If assessment is expired (today > readiness.expires):
  score = 0

Otherwise:
  earnedWeight = sum of earned weights for all in-scope checks
    - deferred checks: excluded from both numerator and denominator
    - done: full weight
    - partial: round(weight × partialCredit)
    - not-done: 0
  
  totalWeight = sum of weights for all in-scope checks (excluding deferred)
  score = round(earnedWeight / totalWeight × 100)

passing = score >= minScore          # minScore defaults to 100
```

**Worked example.** Using the four checks from the example above (weights `20`, `15`, `25`, `10`):
- `legacy-cleanup` has `status: deferred` → excluded from both numerator and denominator
- In-scope weights: `20 + 15 + 25 = 60` (total)
- `dashboard` (`done`, weight `20`) → earns `20`
- `runbook` (`partial`, weight `15`) → earns `round(15 × 0.5) = 8`
- `security-review` (`not-done`, weight `25`) → earns `0`
- Earned weight: `20 + 8 + 0 = 28`
- Score: `round(28 / 60 × 100) = round(46.7) = 47`

If the assessment is expired (current date past `readiness.expires`), the score is `0` regardless of individual check statuses.

**Weights are relative.** Only the *ratio* of weights matters — the score
normalizes by `totalWeight`, so a `weight` of `20` reads as "20%" only when the
weights sum to 100. They can sum to anything; making them sum to 100 just makes
each read as a percentage directly. `pacto explain` and the dashboard show each
check's normalized contribution so you never have to do the math.

**Deferred checks** are excluded entirely from scoring. Use `status: deferred` for
post-launch cleanup tasks or requirements that don't apply to the current service stage.

**The gate (`minScore`)** turns the score from informational into actionable. It
is a readiness threshold: with `minScore: 80` you require 80% weighted completion;
checks below that threshold fail the gate. The gate is evaluated by tooling, not
baked into contract validity:

- `pacto explain` shows `Gate: PASS/FAIL (score N / minScore M)`.
- `pacto validate --readiness` (off by default) **fails** when `score < minScore`.
  It is opt-in because it depends on the current time — making it time-dependent —
  which would otherwise make plain `validate` non-deterministic.
- The operator sets `status.readiness.passing` and the `ReadinessSatisfied`
  condition from the same rule.

Because `minScore` is an authored literal, a [policy](#policies) can still enforce
it org-wide (e.g. require `readiness.minScore >= 80`) — presence rules stay in
policies, the threshold bar lives here.

### Enforcing readiness with policies

The base schema never requires a specific check. Organizational standards are
expressed as [policies](#policies) using standard JSON Schema. For example, to
require a `dashboard` check with `status: done`, `category: observability` and `weight >= 20`:

```json
{
  "type": "object",
  "required": ["readiness"],
  "properties": {
    "readiness": {
      "type": "object",
      "required": ["checks"],
      "properties": {
        "checks": {
          "type": "array",
          "contains": {
            "type": "object",
            "required": ["id", "status", "category", "weight"],
            "properties": {
              "id": { "const": "dashboard" },
              "status": { "const": "done" },
              "category": { "const": "observability" },
              "weight": { "minimum": 20 }
            }
          }
        }
      }
    }
  }
}
```

Combine multiple `contains` under `allOf` to require several checks (e.g.
`dashboard` + `runbook` + `security-review`). Constraints JSON Schema cannot
express — such as "total weight must equal 100" — are left to a future policy
engine.

---

## `metadata`

Optional. Free-form key-value pairs for organizational use. Not validated beyond type.

```yaml
metadata:
  team: payments
  tier: critical
  on-call: "#payments-oncall"
```

`additionalProperties: false` — no extra fields allowed at any level (except inside `metadata`).

!!! tip
    `metadata` is a deliberate extension point for tooling. Platform teams use it to attach signals their CI or deployment systems can read off a contract — for example, infrastructure contracts can carry `metadata.labels` like `platform/provisioner: crossplane` to drive provisioning generically. See [Composition Patterns — Infrastructure contracts](../patterns.md#2-infrastructure-contracts).

---

