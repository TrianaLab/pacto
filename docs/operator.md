---
title: Kubernetes Operator
layout: default
nav_order: 11
---

# Kubernetes Operator
{: .no_toc }

{: .warning }
This page is under construction. Full documentation will be added in a future release.

The [Pacto Operator](https://github.com/TrianaLab/pacto-operator) is a Kubernetes controller that continuously reconciles Pacto contracts against live cluster state. It bridges the gap between build-time contract validation and runtime compliance.

---

## Overview

The operator introduces two Custom Resource Definitions (CRDs) in the `pacto.trianalab.io` API group:

| CRD | Description |
|-----|-------------|
| **Pacto** | Binds a contract (from OCI or inline) to a Kubernetes workload and continuously validates compliance |
| **PactoRevision** | Immutable snapshot of a resolved contract version, created automatically by the operator |

For installation, usage, and development instructions, see the [pacto-operator repository](https://github.com/TrianaLab/pacto-operator).
