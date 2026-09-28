# Configuration and policy

How a service declares the configuration it accepts and the rules it is held to.
The other sections are in [Contract sections](sections.md),
[Dependencies and state](dependencies-and-state.md) and
[Readiness](readiness.md).

## `configurations`

Declares the named configuration **inputs** the service consumes at runtime. It is not a platform provisioning API, Helm deployment values or a Kubernetes ConfigMap — those are related but not interchangeable (see [Reusing a schema you already have](#reusing-a-schema-you-already-have)). Optional — a service with no configuration input may omit this section.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the configuration entry |
| `required` | boolean | Yes | Whether the configuration is mandatory. `true` = its confirmed absence at runtime is a violation; `false` = optional. Has no default — every entry must set it |
| `schema` | string | Conditional | Non-empty. Must reference a file in the bundle. Required if `ref` is not set |
| `ref` | string | Conditional | Non-empty. OCI or local reference to another Pacto contract. Required if `schema` is not set |
| `values` | object | No | Must conform to the schema defined in `schema` |

When `schema` is used, the configuration schema is a local file, and validation checks that it exists in the bundle and parses. When `ref` is used, the entry points at another Pacto contract instead. The reference is checked as a well-formed OCI or local reference, recorded as a reference edge in the graph and pinned by digest in [`pacto.lock`](../lockfile.md). Pacto does not fetch a schema out of the referenced bundle and does not validate anything against it — a `ref` records where the schema is owned, it does not resolve it. **`schema` and `ref` are mutually exclusive**: when `ref` is set, `schema` and `values` must not be present.

Required configuration keys are derived from the JSON Schema's `required` array.

The optional `values` field provides default configuration values validated against the local `schema`. A `ref:` entry carries no `values`; to attach values, vendor a local `schema:`. Values document expected defaults or provide environment-specific overrides via the `--set` and `--values` flags (see [Contract overrides](overrides.md#contract-overrides)).

!!! tip
    `pacto push` packages every file the contract references, the configuration schema included, into a self-contained OCI artifact.

### External configuration schema reference

Instead of vendoring a configuration schema into the bundle, reference another Pacto contract that owns it. By convention the referenced bundle keeps that schema at `configuration/schema.json`, which is where `pacto init` scaffolds it:

```yaml
configurations:
  - name: platform
    ref: oci://ghcr.io/acme/platform-config-pacto:1.0.0
    required: true
```

A platform team publishes one configuration contract and every service references it. [`pacto lock`](../lockfile.md) follows the chain with cycle detection, so a referenced contract that has its own `configurations[].ref` is resolved and pinned to the end of the closure.

!!! tip
    Configuration references create **reference edges** in the dependency graph, distinct from `dependencies[].ref` edges: `pacto graph --with-references` shows them alongside dependencies, `--only-references` shows them alone and the dashboard graph draws them dashed. The graph records these edges without walking them: only `dependencies[].ref` is traversed, so a service coupled to another purely through a shared configuration schema shows no dependents. [`pacto.lock`](../lockfile.md) is where the transitive reference closure is resolved and pinned.

!!! warning
    `pacto push` rejects local configuration references (`file://` and bare paths) — they are for development only, and every ref must use `oci://` before publishing.

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

### Reusing a schema you already have

These artifacts are **related but not interchangeable**, and two of them being
JSON Schema does not make them the same schema:

| Artifact | Describes |
|----------|-----------|
| Service runtime configuration | keys the running service reads (e.g. a ConfigMap it mounts) |
| Platform provisioning API | inputs to a provisioning claim |
| Helm deployment values | inputs to `helm install`/`upgrade` |
| Kubernetes ConfigMap/Secret content | the runtime configuration object itself |

A Helm chart's `values.schema.json` may be reused as a `configurations[].schema`
**only when** the named scope has the same semantic shape as those values, **or**
when an explicit mapping exists between them. Who should own that schema — the
service or the platform — is in
[Composition patterns](../patterns/index.md#configuration-schema-ownership).

### What the Kubernetes collector validates

The Kubernetes integration binds a `configurations[]` scope to a runtime object
only when the Pacto CR sets `spec.target.configBindings`. When it does, it
validates the **decoded content of the bound ConfigMap key** against the
declared schema — the whole
decoded JSON/YAML value at that key, not the ConfigMap object. For a
`Secret` it verifies existence only; Secret values are never read. A `required`
scope whose bound object or key is confirmed absent is a violation
(NonCompliant), and so is a schema mismatch on observed content. A binding that
cannot be observed at all is insufficient evidence (Unknown), not a violation.

---

## `policies`

Defines or references policy constraints for the contract. Optional — services not subject to a policy may omit this section entirely. A policy is a JSON Schema that validates the contract itself, so an organization can state its standards as rules. Examples: require a health capability, require a declared owner, restrict interface visibility or require a readiness gate.

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
`contains` rule, or a [readiness](readiness.md#readiness) gate. A contract is always checked
against its own inline `schema` policies, so this policy contract satisfies the
rule above by declaring `service.owner`.

### Policy as a contract consumer

To adopt a policy, reference the policy contract via OCI:

```yaml
policies:
  - name: platform-policy
    ref: oci://ghcr.io/acme/platform-policy-pacto:1.0.0
```

The `ref` row above says which schemas a referenced contract contributes. Which commands walk that chain, and what happens when a link in it cannot be fetched, is in [Layer 3](validation.md#layer-3-policy-checks).

!!! tip
    Like `configurations[].ref`, policy references create **reference edges** in the dependency graph (`pacto graph --with-references`) and are pinned in [`pacto.lock`](../lockfile.md) alongside dependencies.

!!! warning
    `pacto push` rejects local `policies[].ref` values (`file://` and bare paths) — they are for development only, and every ref must use `oci://` before publishing.

!!! info
    `pacto push` resolves every remote `policies[].ref` before publishing and rejects the push if the contract violates one of those schemas.

A policy author's own bundle carries the schema at `policy/schema.json`, the
fixed path a `policies[].ref` resolves against; see
[bundle structure](index.md#bundle-structure) for where that sits among the
other optional directories.
