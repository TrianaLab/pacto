# Kubernetes Operator
The Pacto Operator is the runtime verification piece of the Pacto system. It continuously checks that deployed services in Kubernetes remain faithful to their contracts.

For installation, configuration, and CRD reference, see the [pacto-operator repository](https://github.com/trianalab/pacto-operator).

---
## Where the operator fits

The operator is the runtime-verification piece of Pacto — see the [docs home](index.md) for how the CLI, dashboard and operator fit together. The CLI and dashboard work with contracts as **declared artifacts** — what a service *should* be. The operator compares those declarations against **observed reality** — what the service *actually is* in a running cluster.

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

The operator compares the declared contract against observed cluster state and emits typed findings across these dimensions:

| Dimension | What it compares | Finding on drift |
|-------|-----------------|------------------|
| **Workload type** | Does the observed workload kind (Deployment/StatefulSet/Job/CronJob/ReplicaSet) match the declared top-level `workload` and `state.type`? | `WORKLOAD_MISMATCH` |
| **State model** | Does the observed storage (PVCs, volumes) align with `state.persistence`? | `PERSISTENCE_MISMATCH` |
| **Interfaces** | Are the declared `interfaces` available on the running workload? | `INTERFACE_ABSENT` |
| **Capabilities** | Are the declared `capabilities` (`health`, `metrics`) observable on the running workload? | `CAPABILITY_ABSENT` |
| **Dependencies** | Are the declared `dependencies` reachable? | `DEPENDENCY_UNREACHABLE` |
| **Configuration** | Is each declared `configurations` scope present, and do observed values match the schema? | `CONFIGURATION_ABSENT` / `CONFIGURATION_MISMATCH` |

Severity is derived from the declaration, not hard-coded per check: a `required` element that is confirmed absent is an **error**, an optional one is a **warning**, and an element that cannot be observed this cycle yields an **Unknown** finding (`EVIDENCE_MISSING` and the rest of the uncertainty family). The operator also records observed runtime facts (workload kind, strategy, container images, storage) in `status.observedRuntime` for context, but it does not assert image, upgrade-strategy or shutdown timing into findings.

A few checks have deliberately shallow semantics worth calling out:

- **Health and metrics capabilities** are tested for *reachability and basic structure* — the health probe expects a healthy HTTP response, and the metrics probe looks for Prometheus exposition markers (`# HELP` / `# TYPE`). Neither validates the response body against the declared OpenAPI/format.
- **Configuration** matching compares observed ConfigMap values against each scope's JSON Schema; it does not resolve `secret://` references.

Each finding carries a code, subject and severity. The operator aggregates them into one of seven contract statuses (worst-first): if the contract fails structural validation it is **Invalid**; otherwise any error-severity finding makes it **NonCompliant**, else any Unknown finding makes it **Unknown**, else any warning makes it **Warning**, else **Compliant**. Two statuses sit outside that runtime ladder:

- **Invalid** — structural validation failed, or the artifact was malformed and could not be parsed
- **NonCompliant** — a confirmed contract-vs-runtime violation (an error-severity finding)
- **Warning** — only warning-severity findings
- **Unknown** — evidence was insufficient to decide (e.g. the element could not be observed this cycle), or status is not yet determined
- **Compliant** — every required assertion is satisfied
- **Reference** — no target workload (the contract is a shared definition, not a deployed service)
- **NotEvaluated** — a valid, targeted contract for which the dashboard has no runtime source (offline OCI/local view); the reconciler itself does not emit this status

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
    - Full OpenAPI conformance of live endpoints
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

In addition to runtime compliance, the operator evaluates a contract's declared **readiness** — a `pactoVersion: "2.0"` feature. Readiness is a **separate dimension** from contract compliance: a low readiness score never changes `ContractStatus`. See the [Contract Reference](contract-reference/sections.md#readiness) for the scoring model (weights, `expires`, `minScore` and `partialCredit` defaults).

When a contract declares `readiness`, the operator writes the derived assessment to `status.readiness`:

- `score`, `minScore`, `passing`, `totalWeight`, `earnedWeight`, `expired`
- per check: effective weight earned based on status

It also sets a single aggregate condition, **`ReadinessSatisfied`** (the gate: `score >= minScore`):

| Status | Reason | Meaning |
|--------|--------|---------|
| `True`  | `Satisfied`     | the readiness score meets `minScore` |
| `False` | `Expired`       | the assessment `expires` date has passed, so every claim earns zero weight |
| `False` | `BelowMinScore` | the score is below `minScore` (e.g. too many not-done claims) |

On gate transitions the operator emits events sparingly: a `Warning` / `ReadinessGateUnmet` when the gate first drops and a `Normal` / `ReadinessRecovered` when it is met again. Contracts that declare no readiness get neither `status.readiness` nor the condition.

The same gate can be enforced at build time: authors run `pacto validate --readiness` (see the [CLI Reference](cli-reference.md)) to fail CI when the score is below `minScore`, while the operator enforces it continuously at runtime.

---

## What the operator is NOT

- **Not the authoring surface** — contracts are authored with the CLI (`pacto init`, `pacto validate`, `pacto push`). The operator consumes them.
- **Not the diff engine** — version comparison and breaking change detection happen in the CLI and dashboard. The operator reports current state, not historical changes.
- **Not the whole system** — it closes the loop the CLI and dashboard open.
- **Not a deployment tool** — it never creates, modifies, or deletes workloads. It observes.
- **Not a generic Kubernetes drift detector** — it specifically checks contract-declared fields. It does not monitor arbitrary resource drift.

---

## Dashboard integration

When `pacto dashboard` detects a Kubernetes cluster with the Pacto CRD installed, it uses the operator's status data as the **k8s** runtime source. This provides:

- Live contract status (Compliant / Warning / NonCompliant / Reference / Unknown / Invalid / NotEvaluated)
- Derived readiness (overall score plus per-claim done/partial/not-done/deferred status and earned weight; the whole assessment carries a single expiry) as a separate dimension
- Reconciliation conditions with timestamps
- Endpoint health and metrics reachability results
- Resource existence checks (Service, Workload)
- Interface and capability availability details (declared vs. observed)
- Observed runtime state (workload kind, strategy, images, storage)
- Declared **configuration and policy content** — the operator extracts each scope's schema properties (and the policy schema's title/description) into status, so the dashboard renders config/policy details even for reference-only contracts with no OCI source available to it
- Contract-vs-runtime comparison rows

The dashboard combines this operator-provided runtime truth with contract truth loaded from OCI, and degrades gracefully to a k8s-only view when registries are unreachable. See [Dashboard Container](dashboard-docker.md) for the k8s+OCI hybrid mode and deployment instructions, and [Architecture](architecture.md) for the source model.

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

- **Helm chart (GitHub):** [pacto-operator/charts/pacto-operator](https://github.com/trianalab/pacto-operator/tree/main/charts/pacto-operator)
- **Artifact Hub:** [pacto-operator on Artifact Hub](https://artifacthub.io/packages/helm/pacto-operator/pacto-operator)

---

## Learn more

- **CRD API reference:** [api-reference.md](https://github.com/trianalab/pacto-operator/blob/main/docs/api-reference.md)
- **Repository:** [pacto-operator on GitHub](https://github.com/trianalab/pacto-operator)
- **CLI reference:** [CLI Reference](cli-reference.md) — author and validate contracts before deploying
- **Dashboard:** [Dashboard Container](dashboard-docker.md) — explore contracts alongside runtime state
- **Platform guide:** [For Platform Engineers](platform-engineers.md) — the full platform workflow
