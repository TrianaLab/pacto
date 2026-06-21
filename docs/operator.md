# Kubernetes Operator
The Pacto Operator is the runtime verification piece of the Pacto system. It continuously checks that deployed services in Kubernetes remain faithful to their contracts.

For installation, configuration, and CRD reference, see the [pacto-operator repository](https://github.com/trianalab/pacto-operator).

---
## Where the operator fits

Pacto is a system made of three complementary pieces:

| Piece | Responsibility | When it runs |
|-------|---------------|--------------|
| **CLI** | Author, validate, diff, explain, publish contracts | Build time / CI |
| **Dashboard** | Explore contracts, graphs, versions, diffs, interfaces | Any time |
| **Operator** | Verify runtime matches the contract | Continuously in-cluster |

The CLI and dashboard work with contracts as **declared artifacts** — what a service *should* be. The operator compares those declarations against **observed reality** — what the service *actually is* in a running cluster.

```
author → validate → publish → explore → verify at runtime
  CLI      CLI        CLI    dashboard     operator
```

---

## What "runtime fidelity" means

Runtime fidelity is the degree to which a deployed service matches its Pacto contract. The operator measures this by:

1. Loading the contract from OCI (or inline in the CRD)
2. Associating it with a target workload in the cluster
3. Comparing declared contract fields against observed Kubernetes state
4. Reporting the result as structured status on the `Pacto` CRD

The operator does **not** modify workloads, restart pods, or change any cluster state. It is purely observational — it tells you whether reality matches the contract, and where it diverges.

---

## What the operator validates today

The operator checks runtime alignment across these dimensions:

| Check | What it compares |
|-------|-----------------|
| **Service existence** | Does a Kubernetes Service exist for the declared service? |
| **Workload existence** | Does a Deployment, StatefulSet, or Job exist matching the declared workload type? |
| **Port alignment** | Do the ports exposed by the Kubernetes Service match the ports declared in the contract's interfaces? Reports missing and unexpected ports. |
| **Workload kind** | Does the observed workload kind (Deployment/StatefulSet) match the declared `runtime.workload` + `runtime.state.type`? |
| **Container image** | Does the running container image match the contract's `imageRef`? |
| **Upgrade strategy** | Does the Deployment/StatefulSet strategy match `runtime.lifecycle.upgradeStrategy`? |
| **Graceful shutdown** | Does `terminationGracePeriodSeconds` match `runtime.lifecycle.gracefulShutdownSeconds`? |
| **State model** | Does the observed storage (PVCs, emptyDir) align with `runtime.state.persistence`? |
| **Health endpoint** | Is the declared `runtime.health.path` reachable and returning a healthy response? |
| **Metrics endpoint** | Is the declared `runtime.metrics.path` reachable? |
| **Scaling** | Do actual replica counts align with declared `scaling.min` / `scaling.max` / `scaling.replicas`? |

A few checks have deliberately shallow semantics worth calling out:

- **Health and metrics endpoints** are tested for *reachability and basic structure* — the health probe expects a healthy HTTP response, and the metrics probe looks for Prometheus exposition markers (`# HELP` / `# TYPE`). Neither validates the response body against the declared OpenAPI/format.
- **Port alignment** matches by port **number** between the contract's interface ports and the Kubernetes Service ports, reporting missing and unexpected ports.
- **Scaling** compares observed replica bounds against `scaling.min`/`max`/`replicas`. Because the contract models `min`/`max` as plain integers, an explicit `0` is indistinguishable from "unset" and is treated as unset — so a declared lower bound of `0` is not reported (see the [scaling reference](contract-reference.md#scaling)).

Each check produces a structured condition on the CRD status with a type, status, reason, and severity. The operator aggregates these into a contract status:

- **Compliant** — all checks pass
- **Warning** — some checks fail (warnings or errors)
- **NonCompliant** — the contract itself has validation errors
- **Reference** — no target workload (the contract is a shared definition, not a deployed service)
- **Unknown** — contract status has not been determined yet

If the operator cannot **observe** the cluster at all (the observation step
itself fails, as opposed to "resources don't exist"), it does not report a
misleading success: it sets the `RuntimeObserved` condition to `False` with
reason `ObservationFailed`, records the reconciliation as failed, and emits a
`Warning` / `ObservationFailed` event — distinct from a clean "service not
found" result.

!!! note
    Contract status reflects whether the service contract is valid and compliant, not whether the service is healthy at runtime.

!!! warning
    The operator does **not** currently validate:
    - Full OpenAPI conformance of live endpoints (it checks reachability and basic structure, not response schemas)
    - JSON Schema validation of live configuration values
    - Dependency compatibility semantics (whether transitive deps satisfy version constraints)
    - `ref`-based policy schema enforcement at runtime

    These are partly by design. The operator runs the contract through the
    **local-only** `Validate()` path with **no resolver**, so `ref`-based
    policies are never fetched or enforced (only locally-compiled `schema`
    policies are). Likewise, contract loading passes an **empty constraint** to
    OCI ref resolution, so a dependency's `compatibility` range is not applied at
    load time. Build-time `pacto validate` (with network access) is where
    `ref`-policy enforcement and constraint-aware resolution happen.

---

## Readiness status

In addition to runtime compliance, the operator evaluates a contract's declared **readiness** — a `pactoVersion: "1.2"` feature (see the [Contract Reference](contract-reference.md#readiness)). Readiness is a **separate dimension** from contract compliance: a low readiness score never changes `ContractStatus`.

When a contract declares `readiness`, the operator computes a derived assessment from the declared check statuses, weights, `expires`, `minScore` and `partialCredit` values against the current time, and writes it to `status.readiness`:

- `score`, `minScore`, `passing`, `totalWeight`, `earnedWeight`, `assessmentExpired`
- per check: effective weight earned based on status

It also sets a single aggregate condition, **`ReadinessSatisfied`** (the gate: `score >= minScore`, where `minScore` defaults to 100):

| Status | Reason | Meaning |
|--------|--------|---------|
| `True`  | `Satisfied`     | the readiness score meets `minScore` |
| `False` | `BelowMinScore` | the score is below `minScore` (e.g. assessment expired or too many not-done checks) |
| `False` | `Invalid`       | the assessment `expires` date is unparseable |

On gate transitions the operator emits events sparingly: a `Warning` / `ReadinessGateUnmet` when the gate first drops and a `Normal` / `ReadinessRecovered` when it is met again. Readiness is a separate dimension — it never changes `ContractStatus`. Contracts that declare no readiness get neither `status.readiness` nor the condition.

---

## What the operator is NOT

- **Not the authoring surface** — contracts are authored with the CLI (`pacto init`, `pacto validate`, `pacto push`). The operator consumes them.
- **Not the diff engine** — version comparison and breaking change detection happen in the CLI and dashboard. The operator reports current state, not historical changes.
- **Not the whole system** — the operator is valuable because it closes the loop. But without the CLI to author and publish contracts, and without the dashboard to explore them, it is just one piece.
- **Not a deployment tool** — it never creates, modifies, or deletes workloads. It observes.
- **Not a generic Kubernetes drift detector** — it specifically checks contract-declared fields. It does not monitor arbitrary resource drift.

---

## Dashboard integration

When `pacto dashboard` detects a Kubernetes cluster with the Pacto CRD installed, it uses the operator's status data as the **k8s** runtime source. This provides:

- Live contract status (Compliant / Warning / NonCompliant / Reference / Unknown)
- Derived readiness (score, per-check Current/Expired status) as a separate dimension
- Reconciliation conditions with timestamps
- Endpoint health and metrics reachability results
- Resource existence checks (Service, Workload)
- Port alignment details (expected vs. observed)
- Observed runtime state (workload kind, strategy, images, storage)
- Declared **configuration and policy content** — the operator extracts each scope's schema properties (and the policy schema's title/description) into status, so the dashboard renders config/policy details even for reference-only contracts with no OCI source available to it
- Contract-vs-runtime comparison rows

The dashboard also **automatically discovers OCI repositories** from the `resolvedRef` fields in Pacto CRD statuses. This means when the dashboard runs in Kubernetes (e.g., as a Deployment alongside the operator), it can load full contract bundles from OCI — providing version history, interface details, and diffs — without needing explicit OCI arguments. Configuration and policy content is available directly from status (above), so it shows even when no OCI bundle can be reached.

If those registries are unreachable or unauthenticated, OCI enrichment **degrades silently** to a k8s-only view: live runtime status, conditions, endpoints, and the status-provided config/policy content still render, but version history and cross-version diffs (which require materialized bundles) are unavailable until the registries become reachable.

The result is a hybrid view: **runtime truth from the operator + contract truth from OCI**, merged in one place.

See [Dashboard Container](dashboard-docker.md) for deployment instructions.

---

## PactoRevision CRDs

The operator creates `PactoRevision` resources to track version history. Each revision records:

- Service name and version
- OCI source reference
- Contract hash
- Timestamp

The dashboard uses these revisions as one input for version history. However, the authoritative source for available versions is the OCI registry — the dashboard queries it directly for the full list of semver tags.

---

## Installation

The operator is distributed as a Helm chart:

- **Helm chart (GitHub):** [pacto-operator/charts/pacto-operator](https://github.com/TrianaLab/pacto-operator/tree/main/charts/pacto-operator)
- **Artifact Hub:** [pacto-operator on Artifact Hub](https://artifacthub.io/packages/helm/pacto-operator/pacto-operator)

---

## Learn more

- **CRD API reference:** [api-reference.md](https://github.com/TrianaLab/pacto-operator/blob/main/docs/api-reference.md)
- **Repository:** [pacto-operator on GitHub](https://github.com/trianalab/pacto-operator)
- **CLI reference:** [CLI Reference](cli-reference.md) — author and validate contracts before deploying
- **Dashboard:** [Dashboard Container](dashboard-docker.md) — explore contracts alongside runtime state
- **Platform guide:** [For Platform Engineers](platform-engineers.md) — the full platform workflow
