---
title: Kubernetes Operator
layout: default
nav_order: 11
---

# Kubernetes Operator
{: .no_toc }

The [Pacto Operator](https://github.com/TrianaLab/pacto-operator) is a Kubernetes controller that continuously reconciles Pacto contracts against live cluster state. It bridges the gap between build-time contract validation and runtime compliance.

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

---

## Overview

The operator introduces two Custom Resource Definitions (CRDs) in the `pacto.trianalab.io` API group:

| CRD | Description |
|-----|-------------|
| **Pacto** | Binds a contract (from OCI or inline) to a Kubernetes workload and continuously validates compliance |
| **PactoRevision** | Immutable snapshot of a resolved contract version, created automatically by the operator |

When a `Pacto` resource is created, the operator:

1. Resolves the contract from an OCI registry (or reads it inline)
2. Parses and validates the contract using the same validation engine as the CLI
3. Observes the target Kubernetes resources (Service, Deployment/StatefulSet)
4. Compares declared state (ports, health endpoints, workload type) against observed state
5. Reports compliance status via structured `.status` fields and standard Kubernetes conditions

---

## Installation

See the [pacto-operator repository](https://github.com/TrianaLab/pacto-operator) for installation instructions.

```bash
# Install CRDs
kubectl apply -f https://raw.githubusercontent.com/TrianaLab/pacto-operator/main/dist/install.yaml
```

### Prerequisites

- Kubernetes v1.11.3+
- `kubectl` configured with cluster access
- OCI registry credentials (if using `contractRef.oci`)

---

## Usage

### Bind a contract to a workload

```yaml
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: payments-api
  namespace: default
spec:
  contractRef:
    oci: ghcr.io/acme/payments-api-pacto
  target:
    serviceName: payments-api
    workloadRef:
      name: payments-api
      kind: Deployment
  checkIntervalSeconds: 300
```

The operator resolves the latest semver tag from the OCI registry, pulls the contract, and begins reconciliation against the target Service and Deployment.

### Reference-only contract (no runtime target)

Omit the `target` field to register a contract without runtime validation. This is useful for shared libraries, external dependencies, or contracts that exist purely as dependency graph nodes:

```yaml
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: shared-config
spec:
  contractRef:
    oci: ghcr.io/acme/platform-config-pacto
```

### Inline contract (development)

For development and testing, embed the contract YAML directly:

```yaml
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: dev-service
spec:
  contractRef:
    inline: |
      pactoVersion: "1.0"
      service:
        name: dev-service
        version: 0.1.0
      interfaces:
        - name: api
          type: http
          port: 8080
  target:
    serviceName: dev-service
```

---

## Status

The operator exposes rich, structured status fields designed for external consumers (dashboards, CLI tools, other controllers):

```bash
$ kubectl get pacto
NAME           PHASE     SERVICE        VERSION   PASSED   FAILED   LAST RECONCILED   AGE
payments-api   Healthy   payments-api   2.1.0     5        0        2m                10m
auth-service   Degraded  auth-service   1.0.0     3        2        1m                5m
```

### Phases

| Phase | Description |
|-------|-------------|
| `Healthy` | All checks pass — contract and runtime are in agreement |
| `Degraded` | Some checks fail — the service is running but drifted from its contract |
| `Invalid` | The contract itself is invalid (structural or cross-field validation errors) |
| `Reference` | No runtime target — the contract exists as a reference only |
| `Unknown` | Initial state before first reconciliation |

### Status fields

The `.status` object includes:

| Field | Description |
|-------|-------------|
| `contract` | Parsed contract metadata (service name, version, owner, image ref, resolved OCI ref) |
| `validation` | Structural validation result (valid/invalid, errors, warnings) |
| `resources` | Existence check for target Service and workload |
| `ports` | Port comparison: expected vs. observed, missing and unexpected ports |
| `endpoints` | Health and metrics endpoint probe results (reachable, status code, latency) |
| `interfaces` | Parsed interface list from the contract |
| `configuration` | Configuration section summary (has schema, ref, value keys, secret keys) |
| `dependencies` | Declared dependency list |
| `policy` | Policy section summary |
| `runtime` | Runtime section (workload type, state, persistence, health, metrics) |
| `scaling` | Scaling section (replicas or min/max) |
| `metadata` | Free-form metadata from the contract |
| `summary` | Precomputed check counts (total, passed, failed) |
| `conditions` | Standard Kubernetes conditions for individual checks |
| `currentRevision` | Name of the active PactoRevision |
| `lastReconciledAt` | Timestamp of the last reconciliation |

---

## PactoRevision

Each time the operator resolves a new contract version, it creates an immutable `PactoRevision` resource:

```bash
$ kubectl get pactorevision
NAME                      VERSION   PACTO          RESOLVED   AGE
payments-api-v2-1-0       2.1.0     payments-api   true       10m
payments-api-v2-0-0       2.0.0     payments-api   true       1d
```

Revisions provide an audit trail of which contract versions were active and when. Each revision records:

- The contract version
- The source (resolved OCI reference or inline flag)
- A SHA-256 hash of the raw contract YAML (for content-change detection)
- Resolution timestamp

---

## Dashboard integration

The `pacto dashboard` command auto-detects Pacto CRs in the cluster when `kubectl` is configured. Services discovered from Kubernetes appear with a **k8s** source badge and include runtime compliance data (phase, check counts, conditions, endpoint status) alongside contract details.

The dashboard merges Kubernetes data with other sources (local directories, OCI cache) using a priority system: Kubernetes runtime data takes precedence over local contract data, which takes precedence over cached OCI baselines. This gives you a unified view across all environments.

---

## Further reading

- [pacto-operator repository](https://github.com/TrianaLab/pacto-operator) — source code, deployment manifests, and development guide
- [Contract Reference]({{ site.baseurl }}{% link contract-reference.md %}) — every contract field and validation rule
- [For Platform Engineers]({{ site.baseurl }}{% link platform-engineers.md %}) — consuming contracts for deployment and CI
