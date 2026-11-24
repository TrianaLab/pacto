# Pacto v1 to v2 Migration Guide

This document is the authoritative guide for migrating Pacto contracts from v1.0 to v2.0.

Pacto 2.0 is a clean-break release that refines the contract model, introduces a compliance evaluation framework and consolidates the service-contract shape around core principles: workload lifecycle is a service concern, capabilities are extensible and interface-bound, and configuration and dependency requirements are explicit.

## Why v2 moves runtime data out of the contract

The single principle behind most of the shape changes below: **the contract defines stable operational intent; runtime-specific observation and execution belong to integrations.** A contract should say what a service *is*, not how a particular platform deploys it or what a particular cluster currently shows. When intent and runtime detail are mixed, the contract drifts every time the deployment changes and cannot be reasoned about independently of the system that runs it.

So v2 sorts each v1 field into where it actually belongs:

| v1 field | Belongs to | v2 home |
|---|---|---|
| `service.image` | Delivery (which artifact to run) | Deployment manifests / CI, not the contract |
| `interfaces[].port` | Deployment (where it is bound) | Deployment manifests; the contract keeps the interface `ref` |
| `runtime.lifecycle` (upgrade strategy, graceful shutdown) | Deployment (how it rolls out) | Deployment manifests |
| `scaling` (replicas, min/max) | Deployment (how it scales) | Deployment manifests / autoscaler config |
| `runtime.workload`, `runtime.state` | Intent (what it is) | Promoted to top-level `workload`, `state` |
| `runtime.health`, `runtime.metrics` | Intent (what it exposes) | The discriminated `capabilities[]` array |
| *(new in v2)* runtime observations | Observation (what it looks like now) | External `Evidence`, evaluated against the contract — never stored in it |

Anything genuinely service-specific that has no first-class field can go in `metadata` (free-form) or `extensions` (namespaced tooling data). Runtime *state* is never declared: it is collected as evidence and compared against the contract by the evaluation engine. The concrete field-by-field steps follow.

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

Pacto 2.0 evaluates contracts against runtime evidence to determine compliance status.

### Contract Status

Contracts have a validity axis and a runtime evaluation axis:

- **Validity**: `Valid` or `Invalid` (structural validation)
- **Runtime Evaluation** (for valid contracts only):
  - `Compliant`: all required assertions satisfied
  - `NonCompliant`: confirmed runtime drift from contract
  - `Unknown`: required assertion could not be evaluated (missing/insufficient evidence)
  - `Warning`: only advisory issues (optional assertions or non-blocking findings)
  - `Reference`: contract declares no workload (reference-only bundle)
  - `NotEvaluated`: the contract declares a workload but was never runtime-evaluated at all — what an offline OCI, cache or local source looks like. The operator never emits it; a targeted contract it evaluated but could not decide is `Unknown`.

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
  name: acme-widget
  version: 2.1.0
  image: ghcr.io/org/acme-widget:2.1.0
  owner:
    team: acme-team

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
      evidence: https://grafana.acme.com/d/acme-widget
      weight: 30
```

### v2 Contract

```yaml
pactoVersion: "2.0"

service:
  name: acme-widget
  version: 2.1.0
  owner:
    team: acme-team

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
      evidence: https://grafana.acme.com/d/acme-widget
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
- [ ] Regenerate lock files if using dependency resolution (`pacto lock` in each bundle)
- [ ] Update operator CRs to add `interfaceBindings` and `configBindings` if runtime evaluation is needed

## Further Reading

- **Schema reference**: `pkg/validation/schema/pacto-v2.0.schema.json` and `pkg/validation/schema/pacto-v2.1.schema.json` (JSON Schema, one per `pactoVersion`)
- **Contract model**: `pkg/contract/contract.go` (Go types)
- **Findings and evidence**: `pkg/finding/finding.go`, `pkg/evidence/evidence.go`
- **Evaluation model**: `pkg/validation/evaluate.go` (pure `Evaluate(contract, evidence) -> (findings, coverage)`)
- **Operator integration**: `integrations/kubernetes/api/v1/pacto_types.go`

## Kubernetes integration Go module path

The Kubernetes integration ships as its own Go module inside the monorepo. Its
module path carries the major-version suffix of the current operator release,
`/v7`: `github.com/trianalab/pacto/integrations/kubernetes/v7`. Consumers that
import the operator packages (for example
`.../integrations/kubernetes/v7/api/v1`) use that path and pull the module
at its nested tag:

```bash
go get github.com/trianalab/pacto/integrations/kubernetes/v7@latest
```

The published artifact coordinate is generated into
`integrations/kubernetes/docs/generated/artifact-hub.md`. The operator image, Helm
chart and documentation keep their own OCI/doc versions and are unaffected by the
Go semantic-import suffix.

## Library and operator majors

This guide covers the contract format. The Go library and the operator have
their own major versions, and each major lists its breaking changes with the
migration in the [changelog](https://pacto.run/latest/changelog/):

- Pacto core 4.0.0 (`github.com/trianalab/pacto/v4`): camelCase JSON
  everywhere, the deprecated v3 API removed and some APIs reshaped. See the
  [`@pacto/core` changelog](https://pacto.run/latest/changelog/#pactocore).
- Operator 6.0.0: removed flags, chart values and unused condition reasons.
  See the
  [`@pacto/k8s-module` changelog](https://pacto.run/latest/changelog/#pactok8s-module).
- Operator 7.0.0: the CRDs graduate to `v1` and the module path moves to
  `/v7`. See [Operator 7: CRDs at v1](#operator-7-crds-at-v1).

## Operator 7: CRDs at v1

Chart 7.0.0 graduates all six Pacto CRDs to `v1`: `pactos`,
`pactorevisions` and `noderuntimeobservations` in `pacto.trianalab.io`, and
`stages`, `operations` and `approvals` in `delivery.pacto.trianalab.io`. Each
CRD stores `v1`. `v1alpha1` stays served with the same schema, and a request
for it returns a warning:

```text
Warning: pacto.trianalab.io/v1alpha1 is deprecated; use v1 (removed in chart 8.0)
```

### What the operator does on startup

The operator rewrites every stored object of the `pacto.trianalab.io` group
as `v1`, and the delivery promoter does the same for the delivery group. A
rewrite changes no field. When every object of a CRD is rewritten, they set
the CRD's `status.storedVersions` to `["v1"]` and log one line:

```text
storage migration: pactos.pacto.trianalab.io rewrote 42 objects as v1; storedVersions [v1alpha1 v1] -> [v1]
```

When the new CRDs arrive after the pod starts, for example under
`crds.install=false` or a GitOps sync order, they retry with a backoff of up
to 10 minutes. No restart is needed. A CRD that is not installed is skipped.

### Check the migration

```bash
kubectl logs deploy/pacto-operator | grep 'storage migration'
kubectl get crd pactos.pacto.trianalab.io -o jsonpath='{.status.storedVersions}'
```

The migration of a CRD is done when the second command prints `["v1"]`.
Read the promoter's log for the delivery CRDs.

When some objects cannot be rewritten, the log names up to 20 of them, each
with the reason the apiserver gave, and `storedVersions` stays unchanged:

```text
storage migration: pactos.pacto.trianalab.io could not rewrite 2 objects ([team-a/orders: Invalid team-b/fraud: Conflict]); see MIGRATION.md
```

A transient reason (`Conflict`, `TooManyRequests`, `Timeout`,
`ServerTimeout` or `InternalError`) needs no action: the next retry rewrites
the object. An object that today's schema rejects (`Invalid`) needs an edit
that makes it valid, or its deletion. A `Forbidden` object is usually refused
by an admission policy. Fix each `Invalid` or `Forbidden` object; the next
retry rewrites it and then patches `storedVersions`.

### The chart refuses to drop a stored version

On `helm install` and `helm upgrade`, the chart refuses to render a CRD that
no longer lists a version the cluster still stores:

```text
pactos.pacto.trianalab.io still stores objects as v1alpha1, which this chart no longer serves. Upgrade to the latest chart of the previous major first and wait until kubectl get crd pactos.pacto.trianalab.io -o jsonpath='{.status.storedVersions}' prints ["v1"]. See MIGRATION.md.
```

`helm template` and template-based GitOps renders, such as Argo CD, skip this
check. For them the apiserver refuses the CRD update:

```text
The CustomResourceDefinition "pactos.pacto.trianalab.io" is invalid: status.storedVersions[0]: Invalid value: "v1alpha1": missing from spec.versions; v1alpha1 was previously a storage version, and must remain in spec.versions until a storage migration ensures no data remains persisted in v1alpha1 and removes v1alpha1 from status.storedVersions
```

Either way, run the same `kubectl get crd` check before you move on. No CRD in
chart 7 drops a version, so this matters from chart 8.0.

### Go importers

The module path is `github.com/trianalab/pacto/integrations/kubernetes/v7`.
The types live in `api/v1` and `api/delivery/v1`. `api/v1alpha1` and
`api/delivery/v1alpha1` hold only the root kinds, whose specs and statuses
are the `v1` types. The `pkg/delivery` wire types that the delivery CRDs embed
follow semantic versioning.

### CLI and dashboard against an older chart

The CLI and dashboard from this release default to `v1` and read the version
a cluster serves, so they keep reading and driving a chart 6 cluster.
Manifests that `pacto init --delivery` writes use `v1` and need chart 7.

### Scoped operators migrate by hand

An operator with `controller.watchNamespaces` set cannot list other
namespaces, so it skips the migration and logs one line. Run this script as a
cluster admin after the upgrade. If any object fails, do not patch
`storedVersions`: fix the object and run the script again.

```bash
set -euo pipefail
for crd in pactos.pacto.trianalab.io pactorevisions.pacto.trianalab.io noderuntimeobservations.pacto.trianalab.io \
           stages.delivery.pacto.trianalab.io operations.delivery.pacto.trianalab.io approvals.delivery.pacto.trianalab.io; do
  kubectl get crd "$crd" >/dev/null 2>&1 || continue
  kubectl get "$crd" -A -o json | kubectl replace -f - \
    && kubectl patch crd "$crd" --subresource=status --type=merge -p '{"status":{"storedVersions":["v1"]}}'
  kubectl get crd "$crd" -o jsonpath='{.status.storedVersions}'; echo
done
```

The last command of each iteration must print `["v1"]`. It confirms the
patch, not how each object is stored: on a control plane with several
apiservers, wait until every apiserver serves the upgraded CRDs (a minute is
usually enough) before you run the script.

## Raising pactoVersion with pacto migrate

Within 2.x, `pacto migrate` raises every `pacto.yaml` in a repository to the
newest `pactoVersion`. Run it with no arguments at the repository root, or
name the directories to read. It raises the version token, removes a
leftover `delivery` section and any `build` block that detection reproduces,
leaves every other byte as written and prints one line per file and a
summary:

```text
billing/pacto.yaml:  kept at 2.3: ENVIRONMENT_VALUES_MISSING at environments[0].file: ...
legacy/pacto.yaml:   pactoVersion "1.0" is not migrated automatically; see https://github.com/TrianaLab/pacto/blob/main/MIGRATION.md
orders/pacto.yaml:   already current (2.5)
payments/pacto.yaml: set pactoVersion to "2.5" (was "2.3")
archive:             skipped (from .pactoignore:1)
1 rewritten, 1 current, 1 kept, 1 manual, 1 skipped
```

- `--dry-run` prints the same lines and writes nothing.
- `--to` raises to an older supported version instead of the newest.
- A file whose raise would add a validation finding keeps its version, and its
  line names the finding.
- Folders a root `.pactoignore` names and folders deeper than 8 levels are
  listed as skipped.
- A read-only `pacto.yaml` is reported as `cannot write` and left alone. The
  command exits 1 only when a file cannot be read or written.
- A leftover `delivery` section is removed: the Stage's `spec.applyRef` names
  the applier.
- A `publish.artifacts[].build` block is removed when `pacto ci --publish`
  detects the same tool and image, and the section holds no other image
  artifact (detection builds one image per folder). Any other block stays,
  the version still rises, and the line says what detection would use.

Moving a contract from 1.x to 2.0 stays the manual migration this document
describes, because the contract shape changes. Those changes are intentional
refinements that require human review (particularly around the mandatory
`required` field semantics for dependencies and configurations).
