# @pacto/k8s-module

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
