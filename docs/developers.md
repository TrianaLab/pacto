---
title: For Developers
layout: default
nav_order: 5
---

# Pacto for Developers
{: .no_toc }

You own the service — and you own the contract. Pacto gives you a structured way to declare your service's operational interface alongside your code, so platform engineers, CI systems, and other teams have an accurate, machine-readable description of what your service needs to run.

No forms. No tickets. No wiki pages that go stale. One YAML file, validated by tooling, versioned in a registry.

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

---

## Your workflow

```mermaid
flowchart LR
    A[Write code] --> B[Define pacto.yaml]
    B --> C[pacto validate]
    C --> D[pacto pack]
    D --> E[pacto push]
    E --> F[CI / Platform picks it up]
```

### 1. Initialize your contract

```bash
pacto init my-service
```

This scaffolds a contract with sensible defaults. Edit `pacto.yaml` to match your service.

### 2. Declare your interfaces (optional)

List every boundary your service exposes. Services with no network interfaces (e.g. batch jobs or shared libraries) may omit this section:

```yaml
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: public
    contract: interfaces/openapi.yaml

  - name: events
    type: event
    visibility: internal
    contract: interfaces/events.yaml
```

Include the actual interface files (OpenAPI specs, protobuf definitions, event schemas) in the bundle.

### 3. Define your runtime semantics (optional)

This is where you tell the platform *how* your service behaves — not how to deploy it, but what it *is*:

```yaml
runtime:
  workload: service

  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low

  health:
    interface: api
    path: /health
```

**Ask yourself:**
- Is my service long-running (`service`) or does it run to completion (`job`)?
- Does it hold local state that survives restarts (`stateful`) or not (`stateless`)?
- Does it keep optional in-memory state like caches (`hybrid`)?
- How critical is the data it handles?

The answers determine how platforms provision infrastructure for your service. See [runtime.state]({{ site.baseurl }}{% link contract-reference.md %}#runtimestate) in the Contract Reference for the full explanation.

### 4. Declare dependencies

If your service depends on other Pacto-enabled services:

```yaml
dependencies:
  - ref: oci://ghcr.io/acme/auth-pacto@sha256:abc123
    required: true
    compatibility: "^2.0.0"

  - ref: oci://ghcr.io/acme/cache-pacto:1.0.0
    required: false
    compatibility: "~1.0.0"

  # Tag omitted — resolves to the highest version matching ^3.0.0
  - ref: oci://ghcr.io/acme/utils-pacto
    required: true
    compatibility: "^3.0.0"
```

During development, you can reference local contracts:

```yaml
dependencies:
  - ref: file://../shared-db
    required: true
    compatibility: "^1.0.0"
```

{: .warning }
Local refs are rejected by `pacto push`. Switch all dependencies to `oci://` references before publishing.

If your service depends on a cloud-managed resource (e.g. a database or message queue), create a minimal Pacto contract representing it and reference it as a dependency. This keeps cloud dependencies explicit and version-tracked.

Use `pacto graph` to visualize your dependency tree.

### 5. Validate before pushing

```bash
pacto validate my-service
```

Validation catches errors in three layers:

1. **Structural** — missing fields, wrong types, invalid enum values
2. **Cross-field** — interface references match, state invariants hold, files exist
3. **Semantic** — strategy consistency warnings

### 6. Pack and push

```bash
pacto pack my-service
pacto push oci://ghcr.io/your-org/my-service-pacto -p my-service
```

If the artifact already exists in the registry, `pacto push` prints a warning and exits without pushing. Use `--force` to overwrite:

```bash
pacto push oci://ghcr.io/your-org/my-service-pacto -p my-service --force
```

---

## Common patterns

### Stateless HTTP API

```yaml
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
scaling:
  min: 2
  max: 10
```

### Stateful service (database proxy, cache)

```yaml
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
    gracefulShutdownSeconds: 60
  health:
    interface: api
    path: /health
scaling:
  min: 3
  max: 5
```

### API with local cache (hybrid)

```yaml
runtime:
  workload: service
  state:
    type: hybrid
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
scaling:
  min: 2
  max: 8
```

A `hybrid` service handles requests statelessly but keeps a local cache or session store. The platform knows it can scale horizontally, but might account for cache warm-up time.

### Fixed-replica service

Use `replicas` instead of `min`/`max` when the service should always run an exact number of instances:

```yaml
scaling:
  replicas: 1
```

### Scheduled job

```yaml
runtime:
  workload: scheduled
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
# No scaling — jobs don't scale horizontally
```

---

## Detecting breaking changes

Before releasing a new version, diff against the previous one:

```bash
$ pacto diff oci://ghcr.io/acme/my-service-pacto:1.0.0 my-service
Classification: BREAKING
Changes (2):
  [BREAKING] interfaces (removed): metrics
  [NON_BREAKING] service.version (modified): service.version modified
```

Integrate `pacto diff` into your CI pipeline to block merges that introduce breaking changes.

{: .tip }
Using GitHub Actions? Check out the official [Pacto CLI action]({{ site.baseurl }}{% link github-actions.md %}).

---

## Tips

- **Version your contract alongside your code.** The `pacto.yaml` lives in your repository.
- **Pin dependency digests in production.** Tags are mutable; digests are not.
- **Keep interface contracts up to date.** OpenAPI specs and protobuf definitions in the bundle should match what your service actually serves.
- **Use `pacto explain` to review.** It produces a human-readable summary of your contract.
- **Use `pacto doc` for rich documentation.** It generates Markdown with architecture diagrams and interface tables. Use `--serve` to view it in the browser.
- **Leverage caching.** OCI bundles are cached locally in `~/.cache/pacto/oci/` and tag listings are cached in memory per command, so repeated `graph`, `doc`, and `diff` commands resolve instantly. Use `--no-cache` to force a fresh pull.
- **Use `--verbose` for debugging.** Pass `-v` to any command to see debug-level logs (OCI operations, resolution steps, cache hits/misses) on stderr.
- **Use metadata for organizational context.** Team ownership, on-call channels, and service tiers go in `metadata`.
