# Configuration and policy

Two fields: `configurations` lists the settings a service reads at runtime, and
`policies` lists the rules its contract must follow. Both point at a JSON Schema,
so tools can check values and contracts before anything ships. The other sections are in [Contract sections](sections.md),
[Dependencies and state](dependencies-and-state.md) and
[Readiness](readiness.md).

## `configurations`

Declares the named configuration **inputs** the service reads at runtime. Platform provisioning APIs, Helm deployment values and Kubernetes ConfigMaps describe related but separate things; see [Reusing a schema you already have](#reusing-a-schema-you-already-have). Optional: a service with no configuration input can omit this section.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the configuration entry |
| `required` | boolean | Yes | Whether the configuration is mandatory. `true` = its confirmed absence at runtime is a violation; `false` = optional. Has no default, so every entry must set it |
| `schema` | string | Conditional | Non-empty. Must reference a file in the bundle. Required if `ref` is not set |
| `ref` | string | Conditional | Non-empty. OCI or local reference to another Pacto contract. Required if `schema` is not set |
| `configuration` | string | No | Non-empty, only with `ref`. Names the configuration of the referenced contract this entry takes values for. pactoVersion 2.6. See [Selecting a configuration](#selecting-a-configuration) |
| `values` | object | No | Must conform to the schema: the local `schema`, or the selected configuration schema of the `ref`. Beside a `ref`, pactoVersion 2.6 |

With `schema`, the configuration schema is a local file, and validation checks that it exists in the bundle and parses. With `ref`, the entry points at another Pacto contract that owns the schema. Pacto fetches that contract, checks the values against the schema of the configuration it selects, records the reference as an edge in the graph and pins it by digest in [`pacto.lock`](../lockfile.md); see [External configuration schema reference](#external-configuration-schema-reference). **`schema` and `ref` are mutually exclusive**, and `configuration` needs a `ref`.

Required configuration keys are derived from the JSON Schema's `required` array.

The optional `values` field provides default configuration values validated against the schema. Beside a `ref`, `values` need pactoVersion 2.6 (`NEEDS_PACTO_2_6` otherwise). Values document expected defaults or provide environment-specific overrides through the `--set` and `--values` flags (see [Contract overrides](overrides.md#contract-overrides)). To check the values each environment uses against the schema, list them in [`environments`](environments.md).

!!! tip
    `pacto push` packages every file the contract references, the configuration schema included, into a self-contained OCI artifact.

### External configuration schema reference

You can reference another Pacto contract that owns a configuration schema, so you keep only one copy of it. By convention the referenced bundle keeps that schema at `configuration/schema.json`, which is where `pacto init` scaffolds it:

```yaml
configurations:
  - name: platform
    ref: oci://ghcr.io/acme/platform-config-pacto:1.0.0
    required: true
```

A platform team publishes one configuration contract and every service references it. [`pacto lock`](../lockfile.md) follows the chain with cycle detection, so a referenced contract that has its own `configurations[].ref` is resolved and pinned to the end of the closure.

In 2.6, `values` beside the `ref` are the defaults, each
[environment](environments.md) file merges on top and Pacto checks the result
against the referenced schema:

```yaml
pactoVersion: "2.6"
configurations:
  - name: database
    ref: oci://ghcr.io/trianalab/pacto/postgresql:16.1.0
    configuration: provisioning
    required: true
    values:
      size: medium
      storageGi: 50
```

In a `"2.0"` to `"2.5"` contract, environment values for a `ref` are checked
too. Only 2.6 reports `ENVIRONMENT_VALUES_MISSING` for a required `ref`
configuration an environment leaves out, because only 2.6 can give defaults.

#### Selecting a configuration

The referenced contract may declare several configurations. Pacto picks one:

| The entry | The referenced contract | Result |
|---|---|---|
| Sets `configuration` | Declares that configuration | That configuration |
| Sets `configuration` | Declares no such configuration | `CONFIG_REF_UNKNOWN_CONFIGURATION`, with a suggestion |
| Omits `configuration` | Declares exactly one | That configuration |
| Omits `configuration` | Declares none | `CONFIG_REF_UNKNOWN_CONFIGURATION` |
| Omits `configuration` | Declares more than one | `CONFIG_REF_AMBIGUOUS` |

Both codes are errors in 2.6 and warnings in older contracts, which can carry
no selector. A selected configuration that is itself a `ref` is followed up to
eight refs deep, and a cycle reports `CONFIG_REF_CYCLE`.

#### Resolution and versions

A configuration reference has no `compatibility`: it pins the exact schema its
values were checked against, by digest, tag or `pacto.lock`. Choose the
provider's version on a `dependencies[]` entry for the same repository. When
that dependency's range excludes the version the reference resolves,
validation reports `CONFIG_REF_VERSION_OUTSIDE_RANGE`.

A reference that cannot be fetched fails `pacto validate` and `pacto push`
when a check depends on it, like a policy reference. The [configuration-refs
stage](validation.md#configuration-references-stage) lists every code and
caller.

!!! tip
    Configuration references create **reference edges** in the dependency graph, separate from `dependencies[].ref` edges. `pacto graph --with-references` shows them alongside dependencies, `--only-references` shows them alone and the dashboard graph draws them dashed. `pacto graph` traverses only `dependencies[].ref`. The [operational graph](../operational-graph-reference.md#configuration-reference-consumers) counts a resolved configuration reference as a consumer, so traversal, dependents and `pacto impact` include services coupled only through a referenced schema. [`pacto.lock`](../lockfile.md) resolves and pins the transitive reference closure.

!!! warning
    `pacto push` rejects local configuration references (`file://` and bare paths). Use them for development and switch every ref to `oci://` before you publish.

### Secret references

Keep secrets out of a contract. Store a reference that the platform resolves at deployment time. The contract declares *what* the service needs; the platform decides *how* to provide it.

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

The `secret://` prefix is a convention. Pacto treats it as an opaque string value. Your platform tooling (Kubernetes operators, Terraform modules, deployment scripts) interprets these references and injects the actual secret at runtime. This keeps sensitive values out of the contract while making the dependency on secrets explicit and auditable.

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

These artifacts are related, and some of them use JSON Schema, but each one
describes something different:

| Artifact | Describes |
|----------|-----------|
| Service runtime configuration | keys the running service reads (for example a ConfigMap it mounts) |
| Platform provisioning API | inputs a platform's provisioning tool reads |
| Helm deployment values | inputs to `helm install`/`upgrade` |
| Kubernetes ConfigMap/Secret content | the runtime configuration object itself |

A Helm chart's `values.schema.json` may be reused as a `configurations[].schema`
**only when** the named scope has the same semantic shape as those values, **or**
when an explicit mapping exists between them. [Composition patterns](../patterns/index.md#configuration-schema-ownership)
explains whether the service or the platform owns that schema.

### What the Kubernetes collector validates

The Kubernetes integration binds a `configurations[]` scope to a runtime object
only when the Pacto CR sets `spec.target.configBindings`. When it does, it
validates the **decoded content of the bound ConfigMap key** against the
declared schema, or against the referenced schema for a configuration that
takes its schema by `ref`. It validates the whole decoded JSON or YAML value at that key.
For a `Secret` it verifies only that the object exists and never reads Secret
values. A `required`
scope whose bound object or key is confirmed absent is a violation
(NonCompliant), and so is a schema mismatch on observed content. A binding that
the operator cannot observe is insufficient evidence (Unknown).

---

## `policies`

Defines or references policy constraints for the contract. Optional: a service with no policy can omit this section. A policy is a JSON Schema that validates the contract itself, so an organization can state its standards as rules. Examples: require a health capability, require a declared owner, restrict interface visibility or require a readiness gate.

When present, each entry must have a `name` and either `schema` or `ref` specified.

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Non-empty identifier for the policy entry |
| `schema` | string | Conditional | Non-empty. Path to a JSON Schema file in the bundle (convention: `policy/schema.json`). Required if `ref` is not set |
| `ref` | string | Conditional | Non-empty. OCI or local reference to another Pacto contract. If the referenced contract declares `policies[]`, those schemas are used directly; otherwise Pacto uses the fixed path `policy/schema.json`. Required if `schema` is not set |
| `target` | string | No | What the policy is evaluated against. `contract` is the only accepted value and the default. The field exists so a later release can add a second target without a breaking change. Any other value fails Layer 1 with `SCHEMA_VIOLATION` |

**`schema` and `ref` are mutually exclusive.** An entry defines its policy inline or references an external one.

### Policy as a contract author

To define a policy, create a JSON Schema that describes constraints on `pacto.yaml` contracts and place it at `policy/schema.json` in the bundle:

```yaml
# pacto.yaml: a policy contract
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
field, for example a [health capability](sections.md#capabilities) through a `capabilities`
`contains` rule or a [readiness](readiness.md#readiness) gate. A contract is always checked
against its own inline `schema` policies, so this policy contract satisfies the
rule above by declaring `service.owner`.

### Policy as a contract consumer

To adopt a policy, reference the policy contract via OCI:

```yaml
policies:
  - name: platform-policy
    ref: oci://ghcr.io/acme/platform-policy-pacto:1.0.0
```

The `ref` row above lists the schemas a referenced contract contributes. [Layer 3](validation.md#layer-3-policy-checks) lists the commands that walk that chain and what happens when a link in it fails to fetch.

The policies apply to the contract that references them. `pacto validate --closure` also checks every contract in its dependency closure against them; see [Dependency closure](validation.md#dependency-closure).

!!! tip
    Like `configurations[].ref`, policy references create **reference edges** in the dependency graph (`pacto graph --with-references`) and are pinned in [`pacto.lock`](../lockfile.md) alongside dependencies.

!!! warning
    `pacto push` rejects local `policies[].ref` values (`file://` and bare paths). Use them for development and switch every ref to `oci://` before you publish.

!!! info
    `pacto push` resolves every remote `policies[].ref` before publishing and rejects the push if the contract violates one of those schemas.

A policy author's own bundle carries the schema at `policy/schema.json`, the
fixed path a `policies[].ref` resolves against; see
[bundle structure](index.md#bundle-structure) for where that sits among the
other optional directories.
