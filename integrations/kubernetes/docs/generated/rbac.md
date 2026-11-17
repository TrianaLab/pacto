<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from helm template of charts/pacto-operator (+ config/rbac/metrics-observation).
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# RBAC

Every table below is rendered from the Helm chart itself, so it is the permission set an install actually creates. A cluster-wide install (the default) creates a `ClusterRole` (`pacto-operator-manager`) bound to the controller's ServiceAccount for what has to be cluster-wide; with `controller.watchNamespaces` set, it is replaced by namespaced Roles. Every install creates a `Role` (`pacto-operator-manager`) in the release namespace for the managed components that run there, a second one of the same name in the node agent's namespace when the agent is on, and a `Role` (`pacto-operator-leader-election`) for leader election. The tables render the release namespace as `pacto-operator-system`.

!!! note

    The repository also contains `config/rbac/role.yaml`, a kubebuilder-generated `manager-role`. It is a different object with a different name and is **not** what `helm install` creates. It belongs to the `config/` kustomize scaffolding, which is not published with a release and is deployed by no test or CI job -- Helm is the only supported install path.

## Always granted (`pacto-operator-manager`)

Present in every cluster-wide install, including one with every managed component disabled. Workloads and their wiring are read-only; the writes are on Pacto's own resources, on events, and on the specific named objects a previous install may have created (so the operator can clean them up after you disable a component).

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `configmaps` | `get`, `list`, `watch` | *not name-restricted* |
| `"" (core)` | `events` | `create`, `patch` | *not name-restricted* |
| `"" (core)` | `namespaces` | `get` | `kube-system` |
| `"" (core)` | `namespaces` | `get` | *not name-restricted* |
| `"" (core)` | `pods` | `get`, `list`, `watch` | *not name-restricted* |
| `"" (core)` | `services` | `get`, `list`, `watch` | *not name-restricted* |
| `apps` | `deployments`, `replicasets`, `statefulsets`, `daemonsets` | `get`, `list`, `watch` | *not name-restricted* |
| `batch` | `cronjobs`, `jobs` | `get`, `list`, `watch` | *not name-restricted* |
| `discovery.k8s.io` | `endpointslices` | `get`, `list`, `watch` | *not name-restricted* |
| `gateway.networking.k8s.io` | `httproutes` | `get`, `list`, `watch` | *not name-restricted* |
| `networking.k8s.io` | `ingresses` | `get`, `list`, `watch` | *not name-restricted* |
| `pacto.trianalab.io` | `noderuntimeobservations` | `delete`, `get`, `list`, `watch` | *not name-restricted* |
| `pacto.trianalab.io` | `pactorevisions` | `create`, `get`, `list`, `watch` | *not name-restricted* |
| `pacto.trianalab.io` | `pactorevisions/status` | `get`, `patch`, `update` | *not name-restricted* |
| `pacto.trianalab.io` | `pactos` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` | *not name-restricted* |
| `pacto.trianalab.io` | `pactos/finalizers` | `update` | *not name-restricted* |
| `pacto.trianalab.io` | `pactos/status` | `get`, `patch`, `update` | *not name-restricted* |
| `rbac.authorization.k8s.io` | `clusterroles`, `clusterrolebindings` | `delete`, `get` | `pacto-dashboard` |

No ClusterRole grants Secrets. The operator reads a pull Secret or a Secret that backs a configuration with `get` in the release namespace and in each of `controller.watchNamespaces`, and anywhere else only the Secrets `controller.secretGrants` names (`namespace/name`), each through a Role limited to that Secret.

The `namespaces` `get` rule limited to `kube-system` reads the cluster id. The one with no name reads the `pacto.trianalab.io/observe` label of a target's namespace, names and labels only, and is granted only while `controller.probing` or `controller.metricsObservation` is `auto` (the default). With `controller.watchNamespaces` each watched namespace's Role carries it instead.

## Always granted in the release namespace (`pacto-operator-manager` Role)

The teardown half of the managed components: `get` and `delete` on the fixed names a previous install may have created there, the node agent's included (releases before the dedicated agent namespace ran it in the release namespace). The one `secrets` `get` rule with no name reads pull Secrets and Secrets that back a configuration in the release namespace.

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `secrets` | `delete`, `get` | `pacto-dashboard-oci-creds` |
| `"" (core)` | `secrets` | `get` | *not name-restricted* |
| `"" (core)` | `serviceaccounts` | `delete`, `get` | `pacto-node-agent` |
| `"" (core)` | `serviceaccounts`, `services` | `delete`, `get` | `pacto-dashboard` |
| `"" (core)` | `services` | `delete`, `get` | `pacto-evidence` |
| `apps` | `daemonsets` | `delete`, `get` | `pacto-node-agent` |
| `apps` | `deployments` | `delete`, `get` | `pacto-dashboard` |
| `apps` | `deployments` | `delete`, `get` | `pacto-evidence` |
| `rbac.authorization.k8s.io` | `roles`, `rolebindings` | `delete`, `get` | `pacto-node-agent` |

## Additionally granted when a managed component is enabled

When a managed component is on, the operator creates and reconciles that component's Deployment, Service, ServiceAccount and RBAC for you, and the chart widens the ClusterRole accordingly. At chart defaults `dashboard.enabled` is **on** and `evidence.enabled` is **off**, so every rule below is what a default install adds for the dashboard. Rendering the chart with `--set dashboard.enabled=false --set evidence.enabled=false` removes every rule in this table.

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `namespaces` | `list` | *not name-restricted* |
| `authorization.k8s.io` | `subjectaccessreviews` | `create` | *not name-restricted* |
| `delivery.pacto.trianalab.io` | `stages`, `operations` | `get`, `list`, `watch` | *not name-restricted* |
| `rbac.authorization.k8s.io` | `clusterroles`, `clusterrolebindings` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` | *not name-restricted* |

Their namespaced half is granted by the release namespace's `pacto-operator-manager` Role, so the operator can create these kinds there and nowhere else:

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `secrets`, `serviceaccounts`, `services` | `create`, `delete`, `get`, `patch`, `update` | *not name-restricted* |
| `apps` | `deployments` | `create`, `delete`, `get`, `patch`, `update` | *not name-restricted* |

!!! warning "This grant allows privilege escalation"

    The rules above include unrestricted `create` on `clusterroles` and `clusterrolebindings`. A subject that can create a ClusterRoleBinding can grant itself any permission in the cluster, so at chart defaults the operator is effectively cluster-admin-capable, not read-only. This is what lets it create the managed components' RBAC.

    If your threat model does not allow that, install with `--set dashboard.enabled=false --set evidence.enabled=false` and deploy those components yourself. The operator then keeps only the *Always granted* table plus narrow `get`/`delete` on the specific objects a previous install may have left behind.

## Additionally granted with the node agent (`nodeAgent.enabled`)

The agent runs in its own namespace (`pacto-node-agent` by default), which the chart creates with the privileged Pod Security level. Cluster-wide, the operator gains a node read (the orphan prune), a read and relabel of that one namespace by name, and a read of the agents' heartbeat Leases (also granted when `controller.kernelEvidence.enabled` is `on`):

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `namespaces` | `get`, `patch` | `pacto-node-agent` |
| `"" (core)` | `nodes` | `get` | *not name-restricted* |
| `coordination.k8s.io` | `leases` | `get`, `list`, `watch` | *not name-restricted* |

Everything it creates for the agent is granted by the `pacto-operator-manager` Role in the agent namespace. The agents' own Role (written by the operator) is `get`, `create` and `patch` on their observations and `get`, `create`, `patch` and `update` on their Leases, and Kubernetes lets the operator grant only what it holds, so it holds those verbs here:

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `serviceaccounts` | `create`, `delete`, `get`, `patch` | *not name-restricted* |
| `apps` | `daemonsets` | `create`, `delete`, `get`, `patch`, `update` | *not name-restricted* |
| `coordination.k8s.io` | `leases` | `create`, `delete`, `get`, `patch`, `update` | *not name-restricted* |
| `pacto.trianalab.io` | `noderuntimeobservations` | `create`, `delete`, `get`, `patch` | *not name-restricted* |
| `pacto.trianalab.io` | `noderuntimeobservations/status` | `create`, `get`, `patch` | *not name-restricted* |
| `rbac.authorization.k8s.io` | `roles`, `rolebindings` | `create`, `delete`, `get`, `patch` | *not name-restricted* |

Where the API serves `ValidatingAdmissionPolicy`, the chart also renders one that lets each agent write only the observation and Lease named after the node in its token, which RBAC cannot express.

## With controller.watchNamespaces

Rendered with `--set 'controller.watchNamespaces={payments,ledger}'`. The chart creates no ClusterRole and no ClusterRoleBinding; the node agent and the dashboard's delivery reads, when on, keep only their cluster-scoped rules. Each watched namespace gets these Roles:

`pacto-dashboard-watch` (`Role` in each watched namespace):

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `services` | `get`, `list`, `watch` | *not name-restricted* |
| `apps` | `deployments`, `statefulsets`, `daemonsets` | `list` | *not name-restricted* |
| `authorization.k8s.io` | `localsubjectaccessreviews` | `create` | *not name-restricted* |
| `batch` | `cronjobs` | `list` | *not name-restricted* |
| `pacto.trianalab.io` | `pactos`, `pactorevisions` | `get`, `list`, `watch` | *not name-restricted* |

`pacto-operator-watch` (`Role` in each watched namespace):

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `configmaps`, `pods`, `services` | `get`, `list`, `watch` | *not name-restricted* |
| `"" (core)` | `events` | `create`, `patch` | *not name-restricted* |
| `"" (core)` | `namespaces` | `get` | *not name-restricted* |
| `"" (core)` | `secrets` | `get` | *not name-restricted* |
| `apps` | `deployments`, `replicasets`, `statefulsets` | `get`, `list`, `watch` | *not name-restricted* |
| `batch` | `cronjobs`, `jobs` | `get`, `list`, `watch` | *not name-restricted* |
| `discovery.k8s.io` | `endpointslices` | `get`, `list`, `watch` | *not name-restricted* |
| `gateway.networking.k8s.io` | `httproutes` | `get`, `list`, `watch` | *not name-restricted* |
| `networking.k8s.io` | `ingresses` | `get`, `list`, `watch` | *not name-restricted* |
| `pacto.trianalab.io` | `pactorevisions` | `create`, `get`, `list`, `watch` | *not name-restricted* |
| `pacto.trianalab.io` | `pactorevisions/status` | `get`, `patch`, `update` | *not name-restricted* |
| `pacto.trianalab.io` | `pactos` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` | *not name-restricted* |
| `pacto.trianalab.io` | `pactos/finalizers` | `update` | *not name-restricted* |
| `pacto.trianalab.io` | `pactos/status` | `get`, `patch`, `update` | *not name-restricted* |

## Namespaced Role (`pacto-operator-leader-election`)

Created in the release namespace and bound to the same ServiceAccount. Used only for the controller-runtime leader election lease.

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `"" (core)` | `configmaps` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` | *not name-restricted* |
| `"" (core)` | `events` | `create`, `patch` | *not name-restricted* |
| `coordination.k8s.io` | `leases` | `create`, `delete`, `get`, `list`, `patch`, `update`, `watch` | *not name-restricted* |

## Optional: metrics-observation ClusterRole

Needed alongside the base role when `--enable-metrics-observation` is set. It is a separate `ClusterRole` (`metrics-observation-role`), never a patch of the base role, so the base grants are untouched. The Helm chart grants the same rules itself when `controller.metricsObservation` is `on`; apply the two objects yourself only for an operator installed without the chart. [Opt-in features](limitations.md#opt-in-features) has the YAML and the caveats.

| API groups | Resources | Verbs | Limited to |
| --- | --- | --- | --- |
| `monitoring.coreos.com` | `servicemonitors`, `podmonitors` | `get`, `list`, `watch` | *not name-restricted* |
