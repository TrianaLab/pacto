# Configuration and policy

How a service declares the configuration it accepts and the rules it is held to.
The other sections are in [Contract sections](sections.md) and
[Dependencies, state and readiness](dependencies-and-state.md).

## `configurations`

Declares the named configuration **inputs** the service consumes at runtime — distinct from a platform provisioning API, Helm deployment values, or a raw Kubernetes ConfigMap/Secret: these are related but **not automatically interchangeable** (see [Configuration schema ownership](../patterns/configuration-schema-ownership.md)). Optional — a service with no configuration input may omit this section.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the configuration entry |
| `required` | boolean | Yes | Whether the configuration is mandatory. `true` = its confirmed absence at runtime is a violation; `false` = optional. Has no default — every entry must set it |
| `schema` | string | Conditional | Non-empty. Must reference a file in the bundle. Required if `ref` is not set |
| `ref` | string | Conditional | Non-empty. OCI or local reference to another Pacto contract. Required if `schema` is not set |
| `values` | object | No | Must conform to the schema defined in `schema` |

When `schema` is used, the configuration schema is a local file, and validation checks that it exists in the bundle and parses. When `ref` is used, the entry points at another Pacto contract instead: the reference is checked as a well-formed OCI or local reference, recorded as a reference edge in the graph and pinned by digest in [`pacto.lock`](../lockfile.md). Pacto does not fetch a schema out of the referenced bundle and does not validate anything against it — a `ref` records where the schema is owned, it does not resolve it. **`schema` and `ref` are mutually exclusive**: when `ref` is set, `schema` and `values` must not be present.

Required configuration keys are derived from the JSON Schema's `required` array.

The optional `values` field provides default configuration values validated against the local `schema`. A `ref:` entry carries no `values`; to attach values, vendor a local `schema:`. Values document expected defaults or provide environment-specific overrides via the `--set` and `--values` flags (see [Contract overrides](overrides.md#contract-overrides)).

!!! tip
    All files referenced by the contract — including the configuration schema — are packaged into the bundle when you run `pacto push`. The bundle is a self-contained OCI artifact.

### External configuration schema reference

Instead of vendoring a configuration schema into the bundle, you can reference another Pacto contract that owns it. By convention the referenced bundle keeps that schema at `configuration/schema.json`, which is where `pacto init` scaffolds it — Pacto records the reference and pins the referenced bundle, and reading the schema out of it is the consumer's job:

```yaml
configurations:
  - name: platform
    ref: oci://ghcr.io/acme/platform-config-pacto:1.0.0
    required: true
```

This enables centralized configuration management — a platform team publishes a single configuration contract, and all services reference it. [`pacto lock`](../lockfile.md) follows the chain: if the referenced contract itself has a `configurations[].ref`, the whole transitive closure is resolved and pinned (with cycle detection), using the same OCI resolution and caching infrastructure as dependencies.

!!! tip
    Configuration references create **reference edges** in the dependency graph, distinct from `dependencies[].ref` edges. Use `pacto graph --with-references` to visualize them, or `pacto graph --only-references` to show only reference edges. In the dashboard graph, reference edges appear as dashed lines. The graph records reference edges, it does not walk them — only `dependencies[].ref` is traversed, so a service coupled to another purely through a shared configuration schema shows no dependents. The lockfile is where the reference closure is resolved.

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

Schema ownership, reusing a Helm `values.schema.json` and what the Kubernetes
collector validates against a bound ConfigMap/Secret are covered in
[Configuration schema ownership](../patterns/configuration-schema-ownership.md).

---

## `policies`

Defines or references policy constraints for the contract. Optional — services not subject to a policy may omit this section entirely. A policy is a JSON Schema that validates the contract itself, enabling platform teams to enforce organizational standards (e.g., require a health capability, enforce interface visibility rules, mandate a declared owner or a readiness gate).

When present, each entry must have a `name` and either `schema` or `ref` specified.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the policy entry |
| `schema` | string | Conditional | Non-empty. Path to a JSON Schema file in the bundle (convention: `policy/schema.json`). Required if `ref` is not set |
| `ref` | string | Conditional | Non-empty. OCI or local reference to another Pacto contract. If the referenced contract declares `policies[]`, those schemas are used directly; otherwise falls back to the fixed path `policy/schema.json`. Required if `schema` is not set |
| `target` | string | No | What the policy is evaluated against. `contract` is the only accepted value and the default, so there is no reason to set it today; it exists so a later release can add a second target without a breaking change. Any other value fails Layer 1 with `SCHEMA_VIOLATION` |

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
field — for example a [health capability](sections.md#capabilities) via a `capabilities`
`contains` rule, or a [readiness](dependencies-and-state.md#readiness) gate. A contract is always checked
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

A policy author's own bundle carries the schema at `policy/schema.json`, the
fixed path a `policies[].ref` resolves against; see
[bundle structure](index.md#bundle-structure) for where that sits among the
other optional directories.
