---
title: Kubernetes Operator
layout: default
nav_order: 11
---

# Kubernetes Operator
{: .no_toc }

{: .warning }
The operator is under active development. For up-to-date installation, configuration, and usage instructions, see the [pacto-operator repository](https://github.com/TrianaLab/pacto-operator).

---

## What the operator does

The [Pacto Operator](https://github.com/TrianaLab/pacto-operator) is a Kubernetes controller that continuously reconciles Pacto contracts against live cluster state. It bridges the gap between **build-time contract validation** (what `pacto validate` does) and **runtime compliance** (whether the deployed service matches its contract).

The operator watches for `Pacto` custom resources in the cluster, pulls the referenced contract from an OCI registry, and compares the contract's declared state against the actual Kubernetes resources.

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

---

## Dashboard integration

When `pacto dashboard` detects a Kubernetes cluster with the Pacto CRD installed, it automatically uses the operator's status data as the **k8s** source. This provides live phase status, conditions, endpoint health results, and resource existence checks directly in the dashboard.

---

## Learn more

For CRD definitions, installation, configuration, and development instructions, see the [pacto-operator repository](https://github.com/TrianaLab/pacto-operator).

See [For Platform Engineers]({{ site.baseurl }}{% link platform-engineers.md %}) for the full platform workflow.
