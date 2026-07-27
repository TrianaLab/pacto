# Configuration schema ownership

Guidance for choosing who owns a `configurations[]` schema and how it maps to a
running system. This is architectural/organizational guidance; the normative
`configurations` field definition lives in the
[Contract reference](../contract-reference/sections.md#configurations).

A `configurations[]` entry declares a **named configuration input** the service's
operational contract requires or accepts. The schema is a JSON Schema document; its
*meaning* depends on who defines it. It is usually a schema you already have rather
than one written for Pacto — but reuse is conditional, not automatic (see below).

## Service-owned input

When a service defines its own configuration schema, the schema expresses **what the
service requires** to run. The author knows which keys the service reads, their
types, and which are mandatory.

```yaml
configurations:
  - name: default
    required: true
    schema: configuration/schema.json
```

The contract carries its own requirements, so each team defines exactly what its
service needs and the bundle deploys on any platform.

## Platform-provided input

When a platform team defines a shared configuration schema, the schema describes a
**configuration input the platform supplies to the service** — not a catalogue of
everything the platform can provide. It is the subset of platform-controlled values
the service actually consumes at runtime.

Two distribution approaches:

**Vendored (local path):** the platform publishes the schema and services copy it
into their bundle at build time:

```yaml
configurations:
  - name: platform
    required: true
    schema: configuration/platform-schema.json
```

**Referenced (OCI):** services reference the platform's configuration contract via
`configurations[].ref`; Pacto resolves the schema from the referenced bundle at the
fixed path `configuration/schema.json`:

```yaml
configurations:
  - name: platform
    ref: oci://ghcr.io/acme/platform-config-pacto:1.0.0
    required: true
```

## Mixed scopes

Because `configurations` is an array, one entry may reference a platform schema and
another define a service-specific schema. `schema` and `ref` are mutually exclusive
within a single entry — choose one per entry.

## Reusing a Helm `values.schema.json` — the precise condition

These are **related but not interchangeable** artifacts, and they must not be
treated as equivalent merely because both are JSON Schema:

| Artifact | Describes |
|----------|-----------|
| Service runtime configuration | keys the running service reads (e.g. a ConfigMap it mounts) |
| Platform provisioning API | inputs to a provisioning claim |
| Helm deployment values | inputs to `helm install`/`upgrade` |
| Kubernetes ConfigMap/Secret content | the runtime configuration object itself |

A Helm chart's `values.schema.json` may be reused as a `configurations[].schema`
**only when** the named Pacto configuration scope has the same semantic shape as
those values, **or** when an explicit mapping exists between them. A deployment
schema and a runtime ConfigMap schema are not the same schema by virtue of both
being JSON Schema. Reuse the one that describes the configuration input the service
actually consumes at runtime, which is what the Pacto scope declares.

## What the Kubernetes collector validates

When the Kubernetes integration binds a `configurations[]` scope to a runtime object
via the Pacto CR's `spec.target.configBindings`, it validates the **decoded content
of the bound ConfigMap key** against the declared schema (the whole decoded
JSON/YAML value at that key, not the ConfigMap object). For a `Secret` it verifies
existence only — Secret values are never read. A `required` scope whose bound object
or key is confirmed absent is a violation (NonCompliant); if the binding cannot be
observed at all, that is insufficient evidence (Unknown), not a violation. A schema
mismatch on observed content is a violation, distinct from insufficient evidence.
