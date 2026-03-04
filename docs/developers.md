---
title: For Developers
layout: default
nav_order: 5
---

# Pacto for Developers

As a developer, you own the service contract. Pacto gives you a structured way to declare your service's operational interface alongside your code — ensuring that platform engineers, CI systems, and other teams have an accurate, machine-readable description of what your service needs to run.

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

### 2. Declare your interfaces

List every boundary your service exposes:

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

### 3. Define your runtime semantics

This is where you tell the platform *how* your service behaves:

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
- How critical is the data it handles?

### 4. Declare dependencies

If your service depends on other Pacto-enabled services:

```yaml
dependencies:
  - ref: ghcr.io/acme/auth-pacto@sha256:abc123
    required: true
    compatibility: "^2.0.0"

  - ref: ghcr.io/acme/cache-pacto:1.0.0
    required: false
    compatibility: "~1.0.0"
```

Use `pacto graph` to visualize your dependency tree.

### 5. Validate before pushing

```bash
pacto validate my-service/pacto.yaml
```

Validation catches errors in three layers:

1. **Structural** — missing fields, wrong types, invalid enum values
2. **Cross-field** — interface references match, state invariants hold, files exist
3. **Semantic** — strategy consistency warnings

### 6. Pack and push

```bash
pacto pack my-service/pacto.yaml
pacto push ghcr.io/your-org/my-service-pacto:1.0.0 -p my-service/pacto.yaml
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
pacto diff ghcr.io/acme/my-service-pacto:1.0.0 my-service/pacto.yaml
```

```
Classification: BREAKING
Changes (2):
  [BREAKING] interfaces (removed): metrics
  [NON_BREAKING] service.version (modified): service.version modified
```

Integrate `pacto diff` into your CI pipeline to block merges that introduce breaking changes.

---

## Tips

- **Version your contract alongside your code.** The `pacto.yaml` lives in your repository.
- **Pin dependency digests in production.** Tags are mutable; digests are not.
- **Keep interface contracts up to date.** OpenAPI specs and protobuf definitions in the bundle should match what your service actually serves.
- **Use `pacto explain` to review.** It produces a human-readable summary of your contract.
- **Use metadata for organizational context.** Team ownership, on-call channels, and service tiers go in `metadata`.
