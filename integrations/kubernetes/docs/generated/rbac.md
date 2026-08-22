<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from helm template of charts/pacto-operator (+ config/rbac/metrics-observation).
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# RBAC

Every table below is rendered from the Helm chart itself, so it is the permission set an install actually creates. The chart creates one cluster-scoped `ClusterRole` (`pacto-operator-manager`) bound to the controller's ServiceAccount, and one namespaced `Role` (`pacto-operator-leader-election`) for leader election.

!!! note

    The repository also contains `config/rbac/role.yaml`, a kubebuilder-generated `manager-role` used by the kustomize deployment under `config/`. It is a different object with a different name and is **not** what `helm install` creates. If you deploy with kustomize rather than Helm, read that file directly.

## Always granted (`pacto-operator-manager`)

Present in every install, including one with every managed component disabled. Workloads and their wiring are read-only; the writes are on Pacto's own resources, on events, and on the specific named objects a previous install may have created (so the operator can clean them up after you disable a component).

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `configmaps` | `get` | *not name-restricted* |
| `"" (core)` | `events` | `create`, `patch` | *not name-restricted* |
| `"" (core)` | `secrets` | `get`, `list`, `watch` | *not name-restricted* |
| `"" (core)` | `secrets` | `delete`, `get` | `pacto-dashboard-oci-creds` |
| `"" (core)` | `serviceaccounts`, `services` | `delete`, `get` | `pacto-dashboard` |
| `"" (core)` | `services` | `get`, `list`, `watch` | *not name-restricted* |
| `"" (core)` | `services` | `delete`, `get` | `pacto-evidence` |
| `apps` | `deployments` | `delete`, `get` | `pacto-dashboard` |
| `apps` | `deployments` | `delete`, `get` | `pacto-evidence` |
| `apps` | `deployments`, `replicasets`, `statefulsets` | `get`, `list`, `watch` | *not name-restricted* |
| `batch` | `cronjobs`, `jobs` | `get`, `list`, `watch` | *not name-restricted* |
| `discovery.k8s.io` | `endpointslices` | `get`, `list`, `watch` | *not name-restricted* |
| `pacto.trianalab.io` | `pactorevisions` | `create`, `get`, `list`, `watch` | *not name-restricted* |
| `pacto.trianalab.io` | `pactorevisions/status` | `get`, `patch`, `update` | *not name-restricted* |
| `pacto.trianalab.io` | `pactos` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` | *not name-restricted* |
| `pacto.trianalab.io` | `pactos/finalizers` | `update` | *not name-restricted* |
| `pacto.trianalab.io` | `pactos/status` | `get`, `patch`, `update` | *not name-restricted* |
| `rbac.authorization.k8s.io` | `clusterroles`, `clusterrolebindings` | `delete`, `get` | `pacto-dashboard` |

## Additionally granted when a managed component is enabled

When a managed component is on, the operator creates and reconciles that component's Deployment, Service, ServiceAccount and RBAC for you, and the chart widens the ClusterRole accordingly. At chart defaults `dashboard.enabled` is **on** and `evidence.enabled` is **off**, so every rule below is what a default install adds for the dashboard. Rendering the chart with `--set dashboard.enabled=false --set evidence.enabled=false` removes every rule in this table.

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `namespaces` | `create`, `get`, `list`, `watch` | *not name-restricted* |
| `"" (core)` | `secrets` | `create`, `delete`, `patch`, `update` | *not name-restricted* |
| `"" (core)` | `serviceaccounts`, `services` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` | *not name-restricted* |
| `apps` | `deployments` | `create`, `delete`, `patch`, `update` | *not name-restricted* |
| `rbac.authorization.k8s.io` | `clusterroles`, `clusterrolebindings` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` | *not name-restricted* |

!!! warning "This grant allows privilege escalation"

    The rules above include unrestricted `create` on `clusterroles` and `clusterrolebindings`. A subject that can create a ClusterRoleBinding can grant itself any permission in the cluster, so at chart defaults the operator is effectively cluster-admin-capable, not read-only. This is what lets it create the managed components' RBAC.

    If your threat model does not allow that, install with `--set dashboard.enabled=false --set evidence.enabled=false` and deploy those components yourself. The operator then keeps only the *Always granted* table plus narrow `get`/`delete` on the specific objects a previous install may have left behind.

## Namespaced Role (`pacto-operator-leader-election`)

Created in the release namespace and bound to the same ServiceAccount. Used only for the controller-runtime leader election lease.

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `configmaps` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` | *not name-restricted* |
| `"" (core)` | `events` | `create`, `patch` | *not name-restricted* |
| `coordination.k8s.io` | `leases` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` | *not name-restricted* |

## Optional: metrics-observation ClusterRole

Needed alongside the base role when `--enable-metrics-observation` is set. It is a separate `ClusterRole` (`metrics-observation-role`), never a patch of the base role, so the base grants are untouched. **The Helm chart does not package it** -- it lives in `config/rbac/metrics-observation/` and is reachable from a kustomize deployment; see [Opt-in features](limitations.md#opt-in-features).

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `monitoring.coreos.com` | `servicemonitors`, `podmonitors` | `get`, `list`, `watch` | *not name-restricted* |
