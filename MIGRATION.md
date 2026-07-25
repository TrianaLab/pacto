# Pacto v1 to v2 Migration Guide

This document is the authoritative guide for migrating Pacto contracts from v1.0 to v2.0.

Pacto 2.0 is a clean-break release that refines the contract model, introduces a compliance evaluation framework and consolidates the service-contract shape around core principles: workload lifecycle is a service concern, capabilities are extensible and interface-bound, and configuration and dependency requirements are explicit.

## Quick Reference

| Aspect | v1 | v2 |
|--------|----|----|
| **Schema version** | `pactoVersion: "1.0"` | `pactoVersion: "2.0"` |
| **Top-level structure** | Nested `runtime` section | Top-level `workload`, `state`, `capabilities` |
| **Interfaces** | `type: http`, `contract:` | `type: openapi`, `ref:` |
| **Capabilities** | Flat `health`/`metrics` under `runtime` | Discriminated `capabilities[]` with optional `binding` |
| **Dependencies** | Had optional `required` field | Mandatory `required: true/false` |
| **Configurations** | No `required` field | Mandatory `required: true/false` |
| **Removed fields** | `runtime.lifecycle`, `scaling`, `service.image` | Removed (out of scope for contracts) |
| **Readiness** | `checks` array | `claims` array |

## Contract Shape Changes

### 1. Schema Version

Update the schema version declaration:

```yaml
# v1
pactoVersion: "1.0"

# v2
pactoVersion: "2.0"
```

### 2. Service Section

**Remove `service.image`** (deployment artifact, not contract concern):

```yaml
# v1
service:
  name: my-service
  version: 1.0.0
  image: ghcr.io/org/my-service:1.0.0  # REMOVE THIS

# v2
service:
  name: my-service
  version: 1.0.0
  # image removed
```

### 3. Interfaces

Change interface `type` from `http` to spec-kind (`openapi`, `asyncapi`, `grpc`) and rename `contract` to `ref`. Remove `port` (runtime binding, not contract concern):

```yaml
# v1
interfaces:
  - name: http-api
    type: http
    port: 8080              # REMOVE
    contract: interfaces/openapi.json  # RENAME to ref
    visibility: public

# v2
interfaces:
  - name: http-api
    type: openapi           # spec-kind: openapi, asyncapi, grpc
    ref: interfaces/openapi.json
    visibility: public
```

### 4. Workload and State (Top-Level)

Move `runtime.workload` and `runtime.state` to top level:

```yaml
# v1
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low

# v2
workload: service

state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
```

Valid `workload` values: `service`, `job`, `scheduled`.

Valid `state.type` values: `stateless`, `stateful`, `hybrid`.

### 5. Capabilities (Health and Metrics)

Convert flat `runtime.health` and `runtime.metrics` to the discriminated `capabilities` array with optional `binding`:

```yaml
# v1
runtime:
  health:
    interface: http-api
    path: /health
  metrics:
    interface: http-api
    path: /metrics

# v2
capabilities:
  - type: health
    binding:
      type: http
      interface: http-api
      path: /health
  - type: metrics
    binding:
      type: http
      interface: http-api
      path: /metrics
```

Standard capability types: `health`, `metrics`.

Extension capabilities require a namespaced `ref` (e.g., `example.com/custom`) and do NOT support `binding` this release:

```yaml
# Extension capability (v2)
capabilities:
  - type: extension
    ref: example.com/custom-capability
```

### 6. Dependencies (Mandatory `required` Field)

Every dependency MUST declare `required: true` or `required: false`:

```yaml
# v1
dependencies:
  - name: postgresql
    ref: oci://ghcr.io/org/postgresql-pacto
    compatibility: "^16.0.0"
    # required was optional in v1

# v2
dependencies:
  - name: postgresql
    ref: oci://ghcr.io/org/postgresql-pacto
    required: true          # MANDATORY in v2
    compatibility: "^16.0.0"
```

`required: true` means the service cannot function without it; `required: false` means the service degrades gracefully when unavailable.

### 7. Configurations (Mandatory `required` Field)

Every configuration MUST declare `required: true` or `required: false`:

```yaml
# v1
configurations:
  - name: app
    schema: configuration/schema.json
    # required was not present in v1

# v2
configurations:
  - name: app
    schema: configuration/schema.json
    required: true          # MANDATORY in v2
```

`required: true` means confirmed absence at runtime is a violation; `required: false` means the configuration is optional.

### 8. Readiness (Rename `checks` to `claims`)

Rename the `readiness.checks` array to `readiness.claims`:

```yaml
# v1
readiness:
  minScore: 80
  expires: "2027-01-31"
  checks:
    - id: dashboard
      # ...

# v2
readiness:
  minScore: 80
  expires: "2027-01-31"
  claims:              # RENAMED from checks
    - id: dashboard
      # ...
```

### 9. Removed Fields

The following v1 fields are REMOVED in v2 (out of contract scope):

- `runtime.lifecycle` (deployment strategy, belongs in deployment manifests)
- `scaling` (runtime scaling behavior, belongs in deployment manifests)
- `service.image` (deployment artifact reference, belongs in deployment manifests)

## Compliance Model Changes

Pacto 2.0 introduces a comprehensive compliance evaluation framework. Contracts are now evaluated against runtime evidence to determine compliance status.

### Contract Status

Contracts have a validity axis and a runtime evaluation axis:

- **Validity**: `Valid` or `Invalid` (structural validation)
- **Runtime Evaluation** (for valid contracts only):
  - `Compliant`: all required assertions satisfied
  - `NonCompliant`: confirmed runtime drift from contract
  - `Unknown`: required assertion could not be evaluated (missing/insufficient evidence)
  - `Warning`: only advisory issues (optional assertions or non-blocking findings)
  - `Reference`: contract declares no target (reference-only bundle)
  - `NotEvaluated`: target declared but no runtime evidence available

### Findings and Evidence

Runtime evaluation produces **findings** with severities:

- `error`: confirmed violation (e.g., `DEPENDENCY_UNREACHABLE`, `CAPABILITY_ABSENT`)
- `unknown`: insufficient or missing evidence (e.g., `EVIDENCE_MISSING`, `COLLECTION_FAILED`)
- `warning`: advisory issues only
- `info`: informational findings

Findings reference **evidence** collected from runtime observations (workload type, persistence, capability endpoints, dependency reachability, configuration presence).

### Evaluation Coverage

Coverage is a metadata metric separate from compliance status. It tracks what percentage of required assertions had conclusive observations (outcome: observed) vs. inconclusive/missing evidence.

## Operator CR Changes

The `pacto.trianalab.io/v1alpha1` `Pacto` CR gains runtime bindings that let the operator observe interfaces and configurations under the compliance model. Bindings nest under `spec.target`.

### Spec Changes

**Interface Bindings** (new, optional):

```yaml
spec:
  target:
    serviceName: my-service
    interfaceBindings:
      - interface: http-api      # contract interfaces[].name
        servicePort: 8080        # Service port name or number
```

Binds declared interface names to Kubernetes Service ports for interface availability observation.

**Config Bindings** (new, optional):

```yaml
spec:
  target:
    serviceName: my-service
    configBindings:
      - configuration: app       # contract configurations[].name
        kind: ConfigMap          # ConfigMap or Secret
        name: my-service-config
      - configuration: secrets
        kind: Secret
        name: my-service-secrets
```

Binds declared configuration names to the ConfigMap/Secret backing each for configuration presence observation.

**Flags**:

- `--stabilization-window`: grace period before a sustained negative becomes a confirmed finding (default 2m)
- `--enable-metrics-observation`: opt-in metrics endpoint probing (default false)

### Status Changes

The `status` section now reports:

```yaml
status:
  contractStatus: Compliant  # or NonCompliant, Unknown, Warning, Invalid, Reference, NotEvaluated
  findings:
    - code: CAPABILITY_ABSENT
      severity: error
      category: RuntimeDrift
      subject: health           # plain string identifying the assertion
      message: "health capability not observed"
  evaluationCoverage:           # metadata only; never changes contractStatus
    required: 5
    evaluated: 4
  observationWindows:           # a list; one entry per assertion with an open negative streak
    - kind: capability
      subject: health
      firstObservedNegativeAt: "2026-07-25T10:00:00Z"
```

## Before and After Example

### v1 Contract

```yaml
pactoVersion: "1.0"

service:
  name: payments-service
  version: 2.1.0
  image: ghcr.io/org/payments-service:2.1.0
  owner:
    team: payments-team

interfaces:
  - name: http
    type: http
    port: 8080
    contract: interfaces/openapi.json
    visibility: internal

configurations:
  - name: platform
    ref: oci://ghcr.io/org/platform-config
  - name: app
    schema: configuration/schema.json

dependencies:
  - name: postgresql
    ref: oci://ghcr.io/org/postgresql-pacto
    required: true
    compatibility: "^16.0.0"

runtime:
  workload: service
  state:
    type: stateful
    persistence:
      scope: shared
      durability: persistent
    dataCriticality: high
  lifecycle:
    upgradeStrategy: rolling
    gracefulShutdownSeconds: 30
  health:
    interface: http
    path: /health
  metrics:
    interface: http
    path: /metrics

scaling:
  replicas: 3

readiness:
  minScore: 80
  expires: "2027-01-31"
  checks:
    - id: dashboard
      type: url
      status: done
      evidence: https://grafana.acme.com/d/payments
      weight: 30
```

### v2 Contract

```yaml
pactoVersion: "2.0"

service:
  name: payments-service
  version: 2.1.0
  owner:
    team: payments-team

interfaces:
  - name: http
    type: openapi
    ref: interfaces/openapi.json
    visibility: internal

configurations:
  - name: platform
    ref: oci://ghcr.io/org/platform-config
    required: true
  - name: app
    schema: configuration/schema.json
    required: true

dependencies:
  - name: postgresql
    ref: oci://ghcr.io/org/postgresql-pacto
    required: true
    compatibility: "^16.0.0"

workload: service

state:
  type: stateful
  persistence:
    scope: shared
    durability: persistent
  dataCriticality: high

capabilities:
  - type: health
    binding:
      type: http
      interface: http
      path: /health
  - type: metrics
    binding:
      type: http
      interface: http
      path: /metrics

readiness:
  minScore: 80
  expires: "2027-01-31"
  claims:
    - id: dashboard
      type: url
      category: observability
      status: done
      evidence: https://grafana.acme.com/d/payments
      weight: 30
```

### Key Changes

1. `pactoVersion` updated to `"2.0"`
2. `service.image` removed
3. Interface `type` changed from `http` to `openapi`
4. Interface `contract` renamed to `ref`
5. Interface `port` removed
6. Configuration and dependency `required` fields added (both `true`)
7. `runtime` section removed; `workload` and `state` moved to top level
8. `runtime.lifecycle` removed
9. `runtime.health` and `runtime.metrics` converted to `capabilities` array with discriminated `binding`
10. `scaling` section removed
11. `readiness.checks` renamed to `readiness.claims`

## Migration Checklist

- [ ] Update `pactoVersion` to `"2.0"`
- [ ] Remove `service.image` if present
- [ ] Update interface `type` from `http` to spec-kind (`openapi`, `asyncapi`, `grpc`)
- [ ] Rename interface `contract` to `ref`
- [ ] Remove interface `port`
- [ ] Add `required: true/false` to every dependency
- [ ] Add `required: true/false` to every configuration
- [ ] Move `runtime.workload` to top-level `workload`
- [ ] Move `runtime.state` to top-level `state`
- [ ] Convert `runtime.health` and `runtime.metrics` to `capabilities` array
- [ ] Remove `runtime.lifecycle` if present
- [ ] Remove `scaling` if present
- [ ] Rename `readiness.checks` to `readiness.claims` if present
- [ ] Run `pacto validate <bundle>` to verify migration
- [ ] Regenerate lock files if using dependency resolution (`go run ./genlocks` for demo bundles)
- [ ] Update operator CRs to add `interfaceBindings` and `configBindings` if runtime evaluation is needed

## Further Reading

- **Schema reference**: `pkg/validation/schema/pacto-v2.0.schema.json` (JSON Schema)
- **Contract model**: `pkg/contract/contract.go` (Go types)
- **Compliance model spec**: `docs/superpowers/specs/2026-07-24-compliance-model-refinement.md`
- **Findings and evidence**: `pkg/finding/finding.go`, `pkg/evidence/evidence.go`
- **Operator integration**: pacto-operator repo, `api/v1alpha1/pacto_types.go`

## No Automated Migration Tool

Pacto 2.0 does NOT provide a `pacto migrate` command. This document is the authoritative migration guide. The contract shape changes are intentional refinements that require human review (particularly around the mandatory `required` field semantics for dependencies and configurations).
