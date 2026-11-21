# @pacto/operator-chart

## 6.6.0

### Minor Changes

- ca50460: Detect what the cluster provides. On start and every 5 minutes the operator writes the registry most namespaces pull from, the cloud, a sign-in provider, an exposure controller with a `--set` line for the dashboard route, whether the Prometheus Operator is served and the node kernels with BTF likely, unlikely or unknown to one ConfigMap, `<release>-detected`, in its namespace. Nothing is turned on. The rows show as inferred under Data sources, in `helm upgrade` notes, in `pacto fleet status` and `pacto fleet providers` with `--detected`, in `pacto_fleet_status` with `pacto mcp --fleet --detected` and in `pacto dashboard` with `PACTO_DASHBOARD_DETECTED`. A missing or stale detection names itself and never makes an answer partial. The chart grants `nodes list` in the cluster-wide ClusterRole and lets the operator create that ConfigMap and get and patch only it. With `controller.watchNamespaces` the registry row is partial, cloud and nodes are unknown and no ClusterRole is added.

## 6.5.0

No changes in this release.

## 6.4.1

No changes in this release.

## 6.4.0

No changes in this release.

## 6.3.0

No changes in this release.

## 6.2.0

No changes in this release.

## 6.1.0

No changes in this release.

## 6.0.1

## 6.0.0

## 5.4.3

## 5.4.2

## 5.4.1

## 5.4.0

## 5.3.0

## 5.2.4

## 5.2.3

## 5.2.2

## 5.2.1

## 5.2.0

## 5.1.2

## 5.1.1

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
