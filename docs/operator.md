---
title: Kubernetes Operator
layout: default
nav_order: 11
---

# Kubernetes Operator
{: .no_toc }

The Pacto Operator is the runtime verification piece of the Pacto system. It continuously checks that deployed services in Kubernetes remain faithful to their contracts.

For installation, configuration, and CRD reference, see the [pacto-operator repository](https://github.com/trianalab/pacto-operator).

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

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

Each check produces a structured condition on the CRD status with a type, status, reason, and severity. The operator aggregates these into a phase:

- **Healthy** — all checks pass
- **Degraded** — some checks fail (warnings or errors)
- **Invalid** — the contract itself has validation errors
- **Reference** — no target workload (the contract is a shared definition, not a deployed service)

{: .warning }
> The operator does **not** currently validate:
> - Full OpenAPI conformance of live endpoints (it checks reachability, not response schemas)
> - JSON Schema validation of live configuration values
> - Dependency compatibility semantics (whether transitive deps satisfy version constraints)
> - Policy schema enforcement at runtime
>
> These are potential future directions. Today, contract-level validation of these fields happens at build time via `pacto validate`.

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

- Live phase status (Healthy / Degraded / Invalid / Reference)
- Reconciliation conditions with timestamps
- Endpoint health and metrics reachability results
- Resource existence checks (Service, Workload)
- Port alignment details (expected vs. observed)
- Observed runtime state (workload kind, strategy, images, storage)
- Contract-vs-runtime comparison rows

The dashboard also **automatically discovers OCI repositories** from the `imageRef` fields in Pacto CRD statuses. This means when the dashboard runs in Kubernetes (e.g., as a Deployment alongside the operator), it can load full contract bundles from OCI — providing version history, interface details, configuration schemas, and diffs — without needing explicit `--repo` flags.

The result is a hybrid view: **runtime truth from the operator + contract truth from OCI**, merged in one place.

See [Dashboard Container]({{ site.baseurl }}{% link dashboard-docker.md %}) for deployment instructions.

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
- **CLI reference:** [CLI Reference]({{ site.baseurl }}{% link cli-reference.md %}) — author and validate contracts before deploying
- **Dashboard:** [Dashboard Container]({{ site.baseurl }}{% link dashboard-docker.md %}) — explore contracts alongside runtime state
- **Platform guide:** [For Platform Engineers]({{ site.baseurl }}{% link platform-engineers.md %}) — the full platform workflow
