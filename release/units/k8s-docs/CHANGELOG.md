# @pacto/k8s-docs

## 5.1.0

## 5.0.0

### Major Changes

- 045f11e: The Kubernetes integration moves into the monorepo — breaking for consumers.

  - The Go module path becomes `github.com/trianalab/pacto/integrations/kubernetes/v5`
    (was `github.com/trianalab/pacto-operator`).
  - It pins the published core module `github.com/trianalab/pacto/v3` at release
    time; the `go.work` workspace resolution is development-only.
  - The integration continues the operator `v4` line as `v5` (image + chart +
    module); public OCI/chart coordinates are preserved.
