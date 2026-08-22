# @pacto/k8s-module

## 5.2.0

### Minor Changes

- 8352060: Run the operational graph in a cluster: an operator-managed Evidence Server and observed-dependency input for the dashboard.
  
  - **Managed Evidence Server.** Set `evidence.enabled` and the operator reconciles
    a separate Evidence Server Deployment and an internal Service, with optional
    Ingress or Gateway API `HTTPRoute` exposure. It is single-writer, so its replica
    count is fixed at one, and it runs the same runtime image as the managed
    dashboard. `evidence.registry.subjects` names the exact immutable contract
    revisions evidence may be reported against; the registry holding them is the
    durable store, so the chart installs no volume, database or bucket of its own.
  - **Observed dependencies.** `dashboard.observation.sources` mounts offline
    OTLP/JSON trace exports read-only into the managed dashboard, so its
    operational graph can reconcile declared dependencies against observed ones.
    Each entry is one named data source with a stable identity, read through a root
    the process cannot follow a symlink out of, never written to and never scanned.
    This is offline input only: no OTLP receiver ships and no collector is deployed.
  - **`insecureRegistries`.** Reach named `host:port` registries over plain HTTP for
    a controlled in-cluster registry, scoped per host so every other registry stays
    HTTPS-only. The controller, the managed dashboard and the managed Evidence
    Server all inherit it.
  
  Also adds a `pacto-dev-gateway` chart that installs Envoy Gateway and a
  `GatewayClass` for local development, so the Gateway API path can be exercised on
  a laptop cluster.
  
  Backwards compatible: every new capability is off by default and no existing
  value, CRD field or status shape changes.

## 5.1.2

### Patch Changes

- bbc7b9c: Rebuild the operator and dashboard container images through the new native per-arch
  build pipeline: each architecture builds on its own runner (no QEMU emulation) and is
  merged into the multi-arch manifest. No functional change to the engine, operator, or
  dashboard — this release ships and validates the faster image pipeline.

## 5.1.1

### Patch Changes

- dd4dab1: Repo-wide audit remediation (engine, CLI, dashboard). Closes the OpenAPI
  breaking-change diff false-negatives — path-item-level parameters are now diffed,
  the request body is deep-diffed so a newly required property is BREAKING, and
  optional→required / added-required parameters are BREAKING — so a BREAKING-only
  release gate can no longer be bypassed. Further security and dashboard fixes land
  in the same PR.

  As the first release since v3.1.0, this also ships the previously-merged but
  unreleased dashboard **Ctrl+C shutdown fix** and the demo version-label fix.

## 5.1.0

### Minor Changes

- b58778a: Unify all published OCI artifacts under the monorepo `ghcr.io/trianalab/pacto/*` namespace.

  - operator image → `ghcr.io/trianalab/pacto/operator`
  - operator chart → `ghcr.io/trianalab/pacto/charts/pacto-operator`
  - dashboard image → `ghcr.io/trianalab/pacto/dashboard`
  - dashboard contract bundle → `ghcr.io/trianalab/pacto/dashboard-contract`
  - demo bundles already live under `ghcr.io/trianalab/pacto/*`

  All packages are now created and owned by this repository. The chart name
  `pacto-operator` and the Artifact Hub repository are preserved (re-point the AH
  repository URL to the new chart coordinate). The previous coordinates remain as
  historical — their already-published versions are unaffected. Go module paths
  (`/v3`, `/v5`) are unchanged.

## 5.0.0

### Major Changes

- 045f11e: The Kubernetes integration moves into the monorepo — breaking for consumers.

  - The Go module path becomes `github.com/trianalab/pacto/integrations/kubernetes/v5`
    (was `github.com/trianalab/pacto-operator`).
  - It pins the published core module `github.com/trianalab/pacto/v3` at release
    time; the `go.work` workspace resolution is development-only.
  - The integration continues the operator `v4` line as `v5` (image + chart +
    module); public OCI/chart coordinates are preserved.
