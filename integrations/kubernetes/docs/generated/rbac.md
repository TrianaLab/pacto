<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from config/rbac/role.yaml + config/rbac/metrics-observation/servicemonitor_rbac.yaml.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# RBAC

The operator's base `ClusterRole` (`manager-role`) is generated from kubebuilder markers into `config/rbac/role.yaml`. It is the exact permission set the controller needs to reconcile Pacto resources and observe runtime state.

## Base ClusterRole (`manager-role`)

| API groups | Resources | Verbs |
| --- | --- | --- |
| `"" (core)` | `configmaps` | `get`, `list`, `watch` |
| `"" (core)` | `events` | `create`, `patch` |
| `"" (core)` | `namespaces` | `create`, `get`, `list`, `watch` |
| `"" (core)` | `secrets`, `serviceaccounts`, `services` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` |
| `apps` | `deployments` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` |
| `apps` | `replicasets`, `statefulsets` | `get`, `list`, `watch` |
| `batch` | `cronjobs`, `jobs` | `get`, `list`, `watch` |
| `discovery.k8s.io` | `endpointslices` | `get`, `list`, `watch` |
| `pacto.trianalab.io` | `pactorevisions` | `create`, `get`, `list`, `watch` |
| `pacto.trianalab.io` | `pactorevisions/status`, `pactos/status` | `get`, `patch`, `update` |
| `pacto.trianalab.io` | `pactos` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` |
| `pacto.trianalab.io` | `pactos/finalizers` | `update` |
| `rbac.authorization.k8s.io` | `clusterrolebindings`, `clusterroles` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` |

## Optional: metrics-observation ClusterRole

Applied ALONGSIDE the base role only when `--enable-metrics-observation` is set. It is a separate `ClusterRole` (`metrics-observation-role`), never a patch of `manager-role`, so the base grants are untouched.

| API groups | Resources | Verbs |
| --- | --- | --- |
| `monitoring.coreos.com` | `servicemonitors`, `podmonitors` | `get`, `list`, `watch` |
