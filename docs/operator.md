---
title: Kubernetes Operator
layout: default
nav_order: 11
---

# Kubernetes Operator
{: .no_toc }

{: .warning }
This page covers the operator's design and integration with Pacto core. For installation, configuration, and development instructions, see the [pacto-operator repository](https://github.com/TrianaLab/pacto-operator).

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

---

## What the operator does

The [Pacto Operator](https://github.com/TrianaLab/pacto-operator) is a Kubernetes controller that continuously reconciles Pacto contracts against live cluster state. It bridges the gap between **build-time contract validation** (what `pacto validate` does) and **runtime compliance** (whether the deployed service matches its contract).

The operator watches for `Pacto` custom resources in the cluster, pulls the referenced contract from an OCI registry, and compares the contract's declared state against the actual Kubernetes resources.

---

## CRDs

The operator introduces two Custom Resource Definitions in the `pacto.trianalab.io` API group:

| CRD | Description |
|-----|-------------|
| **Pacto** | Binds a contract (from OCI or inline) to a Kubernetes workload and continuously validates compliance |
| **PactoRevision** | Immutable snapshot of a resolved contract version, created automatically by the operator |

---

## What it checks

The operator validates the following aspects of a deployed service against its contract:

| Check | Description |
|-------|-------------|
| **Service existence** | A Kubernetes Service matching the contract name exists |
| **Workload existence** | A Deployment or StatefulSet matching the contract name exists |
| **Port matching** | Declared interface ports match the ports exposed by the Kubernetes Service |
| **Endpoint health** | Health and metrics endpoints are reachable and return expected status codes |
| **Contract validity** | The contract itself passes structural, cross-field, and semantic validation |

### What it does NOT yet check

The operator does not currently validate:

- **State model compliance** — whether a stateful service is deployed as a StatefulSet with PVCs
- **Scaling bounds** — whether HPA min/max matches contract scaling constraints
- **Upgrade strategy** — whether the deployment strategy matches `runtime.lifecycle.upgradeStrategy`
- **Configuration values** — whether environment variables satisfy the configuration schema
- **Dependency availability** — whether declared dependencies are reachable

These checks are planned for future releases.

---

## How it complements Pacto core

Pacto core (`pacto validate`, `pacto diff`, `pacto graph`) operates at **build time and CI time** — it validates contracts before they are deployed. The operator extends this to **runtime**:

```
Build time                          Runtime
┌──────────────────────┐            ┌────────────────────────────┐
│  pacto validate      │            │  Pacto Operator            │
│  pacto diff          │   push     │  - Watches Pacto CRDs      │
│  pacto push          │ ────────>  │  - Pulls contracts from OCI│
│  pacto graph         │            │  - Validates against live   │
│                      │            │    cluster state            │
└──────────────────────┘            └────────────────────────────┘
```

The operator produces structured status fields on the `Pacto` CRD:

- **Phase** — `Healthy`, `Degraded`, `Invalid`, or `Reference` (contract-only, no workload target)
- **Conditions** — standard Kubernetes conditions with transition timestamps
- **Checks summary** — passed/total/failed check counts

---

## Dashboard integration

When `pacto dashboard` detects a Kubernetes cluster with the Pacto CRD installed, it automatically uses the operator's status data as the **k8s** source. This provides:

- **Live phase status** from operator reconciliation (Healthy, Degraded, Invalid)
- **Conditions** with last transition times
- **Endpoint health** results (HTTP status codes, latency, errors)
- **Resource existence** checks (Service, Deployment/StatefulSet)
- **Port matching** results (expected vs observed ports)

Services with phase `Reference` (contracts without a workload target) appear as **Unmonitored** in the dashboard — they are valid contracts used as shared definitions or dependency references.

---

## For platform engineers

The operator fits into the platform engineering workflow as the runtime enforcement layer:

1. **Developers** write contracts and push them to an OCI registry
2. **Platform teams** create `Pacto` CRDs that bind contracts to workloads
3. **The operator** continuously validates compliance and surfaces issues via CRD status
4. **The dashboard** aggregates operator data with OCI and local sources for a unified view

See [For Platform Engineers]({{ site.baseurl }}{% link platform-engineers.md %}) for the full platform workflow, and the [pacto-operator repository](https://github.com/TrianaLab/pacto-operator) for installation and configuration.
