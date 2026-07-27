# Contract sections

## `pactoVersion`

The contract specification version. The only supported value is `"2.0"`.

```yaml
pactoVersion: "2.0"
```

Every contract is validated against the single tracked JSON Schema,
[`pacto-v2.0.schema.json`](https://github.com/TrianaLab/pacto/blob/main/pkg/validation/schema/pacto-v2.0.schema.json).
Any other value is a hard error (`UNSUPPORTED_PACTO_VERSION`).

---

## `service`

Identifies the service.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Pattern: `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` |
| `version` | string | Yes | Valid semver (e.g., `2.1.0`) |
| `owner` | [OwnerInfo](#ownerinfo) | No | Object only (string form removed) |

`service` carries identity only — there is no `image` or `chart` field. How a
service is built and deployed is a delivery concern that lives outside the
contract.

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

Declares the service's communication boundaries. Optional — a service with no network interfaces (e.g. a batch job or shared library) may omit this section entirely. The `ref` field points at the spec you already publish — an OpenAPI document, an AsyncAPI document or a gRPC service descriptor — so Pacto references your existing interface rather than redefining it.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty. Must be unique across interfaces |
| `type` | string | Yes | Enum: `openapi`, `asyncapi`, `grpc` |
| `ref` | string | Yes | Non-empty. Path to the spec file within the bundle |
| `visibility` | string | No | Enum: `public`, `internal`. Default: `internal` |

### Interface types

| Value | Spec kind |
|---|---|
| `openapi` | OpenAPI document for HTTP/REST traffic |
| `asyncapi` | AsyncAPI document for event-driven communication |
| `grpc` | gRPC service descriptor |

!!! note
    Interface names must be unique within a contract. Every interface requires a `ref` (`INTERFACE_REF_REQUIRED` otherwise), and the referenced file must exist in the bundle and parse as JSON or YAML (`FILE_NOT_FOUND` / `INVALID_INTERFACE_SPEC` otherwise). There is no `port` field — ports are a deployment concern. Health and metrics endpoints are declared as [capabilities](#capabilities), not interfaces.

---

## `configurations`

Defines the service's configuration model. Optional — services with no configuration schema may omit this section entirely.

The `configurations` section is an array of named configuration entries:

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the configuration entry |
| `required` | boolean | Yes | Whether the configuration is mandatory. `true` = its confirmed absence at runtime is a violation; `false` = optional. Has no default — every entry must set it |
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
    required: true
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
    required: true
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

Who owns a configuration schema (service-owned vs platform-provided vs mixed
scopes), the precise condition for reusing a Helm `values.schema.json`, and exactly
what the Kubernetes collector validates against a bound ConfigMap/Secret are covered
in the pattern [Configuration schema ownership](../patterns/configuration-schema-ownership.md).

---

## `policies`

Defines or references policy constraints for the contract. Optional — services not subject to a policy may omit this section entirely. A policy is a JSON Schema that validates the contract itself, enabling platform teams to enforce organizational standards (e.g., require a health capability, enforce interface visibility rules, mandate a declared owner or a readiness gate).

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
pactoVersion: "2.0"
service:
  name: platform-policy
  version: 1.0.0
  owner:
    team: platform
policies:
  - name: platform-policy
    schema: policy/schema.json
```

Example policy schema (`policy/schema.json`) requiring every contract to declare an owner:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["service"],
  "properties": {
    "service": {
      "type": "object",
      "required": ["owner"]
    }
  }
}
```

A policy schema validates the contract itself, so it can require any contract
field — for example a [health capability](#capabilities) via a `capabilities`
`contains` rule, or a [readiness](#readiness) gate. A contract is always checked
against its own inline `schema` policies, so this policy contract satisfies the
rule above by declaring `service.owner`.

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
| `required` | boolean | Yes | Whether the dependency is mandatory. Has no default — every entry must set it |
| `compatibility` | string | Yes | Non-empty. Valid semver constraint |

!!! note
    `required` is mandatory (no default) — every dependency must declare it. `true` means the service cannot function without the dependency; `false` means it degrades gracefully when the dependency is unavailable. It is a declaration of *intent* that downstream consumers act on (the dashboard uses it to compute blast radius; deployment tooling may use it to gate rollout), not a deployment-time guard Pacto itself enforces.

### Dependency reference schemes

| Scheme | Example | Description |
|--------|---------|-------------|
| `oci://` | `oci://ghcr.io/acme/auth-pacto:1.0.0` | OCI registry reference (required for `pacto push`) |
| `oci://` (no tag) | `oci://ghcr.io/acme/auth-pacto` | Resolved to the highest semver tag satisfying `compatibility` |
| `file://` | `file://../shared-db` | Local filesystem path |
| *(bare path)* | `../shared-db` | Local filesystem path (shorthand for `file://`) |

When an `oci://` reference omits the tag, pacto queries the registry for available tags and selects the highest semver version that satisfies the `compatibility` constraint. For example, with `compatibility: "^2.0.0"` and available tags `1.0.0`, `2.0.0`, `2.3.0`, `3.0.0`, pacto resolves to `2.3.0`. Tag listings are cached in memory for the duration of the command, so multiple dependencies pointing to the same repository only trigger a single registry query.

!!! note
    Validation rejects OCI references whose tag or digest is malformed. Tags must follow the OCI tag grammar (`[A-Za-z0-9_][A-Za-z0-9._-]{0,127}`), and digests must be well-formed (`sha256:<64 hex>` or `sha512:<128 hex>`). The same check applies to config and policy refs.

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

## `workload`

Top-level string describing the execution pattern of the workload. Optional. Enum: `service`, `job`, `scheduled`.

| Value | Description |
|-------|-------------|
| `service` | A long-running process that serves requests continuously |
| `job` | A one-shot task that runs to completion and then exits |
| `scheduled` | A task that runs on a recurring schedule (e.g. cron) |

```yaml
workload: service
```

---

## `state`

Top-level section declaring how the service manages state. Optional — a minimal contract (e.g. a lightweight dependency declaration) may omit it entirely. Instead of platforms guessing whether a service needs persistent storage or stable network identity, the contract declares it explicitly.

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

### Persistence

| Field | Type | Required | Enum values |
|-------|------|----------|-------------|
| `scope` | string | Yes | `local`, `shared` |
| `durability` | string | Yes | `ephemeral`, `persistent` |

- **`local`** — data is confined to a single instance. Not shared across replicas.
- **`shared`** — data is shared across all instances via a common store.
- **`ephemeral`** — data can be lost on restart without impact. Caches, temp files, reconstructible state.
- **`persistent`** — data must survive restarts. Requires durable storage.

### State invariants

| Condition | Constraint |
|---|---|
| `type: stateless` | `durability` must be `ephemeral` |

A stateless service with persistent storage is a contradiction — the JSON Schema catches it.

```yaml
state:
  type: stateful
  persistence:
    scope: shared
    durability: persistent
  dataCriticality: high
```

---

## `capabilities`

Optional. Declares standard observability capabilities (`health`, `metrics`) or custom `extension` capabilities. Health and metrics endpoints are capabilities, not interfaces — this is where you tell the platform a service exposes them.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `type` | string | Yes | Enum: `health`, `metrics`, `extension` |
| `ref` | string | Conditional | Required for `extension` only. A namespaced identifier (e.g. `example.com/custom`). Not allowed for `health`/`metrics` |
| `binding` | [Binding](#capability-binding) | No | Standard types only (`health`/`metrics`). Binds the endpoint to a declared interface. Not allowed for `extension` |

### Capability binding

Binds a standard capability endpoint (`health`/`metrics`) to a declared interface so a collector can probe it. `binding.type` is a **closed set — `http` is the only supported transport** this release; an unknown transport fails structural validation (the enum does not advertise unimplemented transports).

Semantics — how a binding resolves:

- `binding.type: http` means the capability is probed over **HTTP**.
- `binding.interface` names the declared interface that provides the **address/port anchor** for the probe — the platform-neutral counterpart of the Kubernetes CR's `spec.target.interfaceBindings[].interface`, which resolves to a concrete Kubernetes Service port. It does **not** claim the capability path is part of that interface's OpenAPI/AsyncAPI/gRPC specification.
- `binding.path` is relative to the resolved endpoint.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `type` | string | Yes | Enum: `http` (only supported transport; any other value fails validation) |
| `interface` | string | Yes | Must match a declared `interfaces[].name` — the interface whose platform binding provides the address/port (`CAPABILITY_INTERFACE_UNKNOWN` otherwise) |
| `path` | string | No | Application path relative to the resolved endpoint. Must start with a single `/` and carry no scheme/host/fragment (`CAPABILITY_PATH_INVALID` otherwise) |

```yaml
capabilities:
  - type: health
    binding:
      type: http
      interface: rest-api
      path: /health
  - type: metrics
    binding:
      type: http
      interface: rest-api
      path: /metrics
  - type: extension
    ref: example.com/tracing
```

A `health` or `metrics` capability may be declared with no binding: it is a valid declaration for another collector to verify, but the current Kubernetes integration cannot actively verify an unbound capability and reports it as Unsupported/Unknown. An `extension` capability requires a namespaced `ref` and must not declare a binding.

---

## `readiness`

Optional. A `pactoVersion: "2.0"` feature. Declares operational readiness
state for the service in a provider-neutral way. Each claim has a completion status,
optional category and weight. The assessment includes an expiry date and scoring
configuration. Pacto computes a readiness score from claim statuses and weights.

```yaml
readiness:
  expires: "2027-06-30"  # assessment-level expiry (YYYY-MM-DD)
  minScore: 80           # gate: the derived score must be >= this (omitted ⇒ 100)
  partialCredit: 0.5     # weight multiplier for partial claims (omitted ⇒ 0.5)

  claims:
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

`readiness.expires`, `readiness.claims[]` and per-claim `status` are required when `readiness`
is present. `readiness.minScore`, `readiness.partialCredit` and `readiness.history[]` are optional.

**`readiness` fields:**

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `expires` | string | Yes | Assessment-level expiry as a strict `YYYY-MM-DD` date. If the current date is past this date, ALL claims earn 0 weight regardless of status. Parsing is exact round-trip: zero-padded fields required and the value must re-serialize unchanged, so `2026-1-1`, RFC 3339 timestamps and impossible dates (e.g. `2026-02-30`) are rejected. |
| `minScore` | integer | No | Gate threshold on the same 0–100 scale as the score. Omitted ⇒ `100` (full compliance required). Enforced by `pacto validate --readiness` and the operator. |
| `partialCredit` | number | No | Multiplier for `partial` status claims (0.0–1.0). Omitted ⇒ `0.5` (half credit). |
| `claims` | [Claim](#claim-fields)[] | Yes | At least one claim. |
| `history` | [HistoryEntry](#history-entry)[] | No | Audit trail of readiness assessment changes. |

### Claim fields

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `id` | string | Yes | Stable readiness requirement id. Pattern: `^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`. Unique within the contract. Policies usually target this field. |
| `type` | string | Yes | Enum: `url`, `document`, `ticket`, `report`, `artifact`, `identifier`, `other`. Evidence type. |
| `status` | string | Yes | Enum: `done`, `partial`, `not-done`, `deferred`. Completion state for the claim. |
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

The service owner is declared at the contract level, so readiness claims carry no
per-claim owner. The readiness score is **not** stored in the contract — it is
computed by tooling (`pacto explain`, the dashboard and the operator) from claim
statuses, weights, the assessment expiry and the current time.

### Readiness score and gate

```text
If assessment is expired (today > readiness.expires):
  score = 0

Otherwise:
  earnedWeight = sum of earned weights for all in-scope claims
    - deferred claims: excluded from both numerator and denominator
    - done: full weight
    - partial: round(weight × partialCredit)
    - not-done: 0

  totalWeight = sum of weights for all in-scope claims (excluding deferred)
  score = round(earnedWeight / totalWeight × 100)

passing = score >= minScore          # minScore defaults to 100
```

**Worked example.** Using the four claims from the example above (weights `20`, `15`, `25`, `10`):
- `legacy-cleanup` has `status: deferred` → excluded from both numerator and denominator
- In-scope weights: `20 + 15 + 25 = 60` (total)
- `dashboard` (`done`, weight `20`) → earns `20`
- `runbook` (`partial`, weight `15`) → earns `round(15 × 0.5) = 8`
- `security-review` (`not-done`, weight `25`) → earns `0`
- Earned weight: `20 + 8 + 0 = 28`
- Score: `round(28 / 60 × 100) = round(46.7) = 47`

If the assessment is expired (current date past `readiness.expires`), the score is `0` regardless of individual claim statuses.

**Weights are relative.** Only the *ratio* of weights matters — the score
normalizes by `totalWeight`, so a `weight` of `20` reads as "20%" only when the
weights sum to 100. They can sum to anything; making them sum to 100 just makes
each read as a percentage directly. `pacto explain` and the dashboard show each
claim's normalized contribution so you never have to do the math.

**Deferred claims** are excluded entirely from scoring. Use `status: deferred` for
post-launch cleanup tasks or requirements that don't apply to the current service stage.

**The gate (`minScore`)** turns the score from informational into actionable. It
is a readiness threshold: with `minScore: 80` you require 80% weighted completion;
a score below that threshold fails the gate. The gate is evaluated by tooling, not
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

The base schema never requires a specific claim. Organizational standards are
expressed as [policies](#policies) using standard JSON Schema. For example, to
require a `dashboard` claim with `status: done`, `category: observability` and `weight >= 20`:

```json
{
  "type": "object",
  "required": ["readiness"],
  "properties": {
    "readiness": {
      "type": "object",
      "required": ["claims"],
      "properties": {
        "claims": {
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

Combine multiple `contains` under `allOf` to require several claims (e.g.
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
    `metadata` is a deliberate extension point for tooling. Platform teams use it to attach signals their CI or deployment systems can read off a contract — for example, infrastructure contracts can carry `metadata.labels` like `platform/provisioner: crossplane` to drive provisioning generically. See [Composition Patterns — Infrastructure contracts](../patterns/infrastructure-contracts.md).

---

## `extensions`

Optional. A free-form object for forward-compatible, namespaced extension data that is not part of the core contract model. Distinct from `metadata` (free-form organizational key-value pairs): `extensions` is reserved for structured data that future Pacto features or third-party tooling may interpret. Keys should be namespaced (e.g. a domain) to avoid collisions. Not validated beyond type this release.

```yaml
extensions:
  example.com/custom-tool:
    enabled: true
```

---

