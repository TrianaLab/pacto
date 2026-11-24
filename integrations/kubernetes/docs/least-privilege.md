# Run with least privilege

## Secrets

The operator reads no Secret across the cluster. It reads a pull secret or the
Secret behind a contract configuration in the release namespace, in each
watched namespace and wherever `controller.secretGrants` names it as
`namespace/name`. Each entry renders a Role limited to that one Secret. Outside
those grants a pull secret sets `ContractValid=Unknown` with reason
`PullSecretNotGranted`, and a Secret-backed configuration reads
`OBSERVATION_UNSUPPORTED` with the missing grant
([Limitations](limitations.md#secret-backed-configuration-evidence)).

## Watch a list of namespaces

To watch a list of namespaces instead of the whole cluster:

```bash
helm install pacto oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace \
  --set 'controller.watchNamespaces={payments,ledger}'
kubectl get clusterrole | grep pacto   # pacto-pacto-operator-pacto-operator-system-dashboard-tokenreview
```

The chart then renders a Role and a RoleBinding in each listed namespace for
the operator, the dashboard and `pacto-viewer`, and one ClusterRole: the
dashboard's `create tokenreviews` grant for token sign-in, which has no
namespaced form and reveals only whose token the dashboard already holds.
Two features need more: the node agent and the dashboard with delivery on,
whose delivery views read across the cluster. The dashboard Overview starts with "Watching 2
namespaces (ledger and payments)." Every fleet answer is `partial`, and a
service in any other namespace is unknown.

The [hardened profile](profiles.md#hardened) makes this the whole install.

In both install modes the dashboard lists Deployments, StatefulSets,
DaemonSets and CronJobs, with `list` only, to show them as inferred services.
Listing them lets its ServiceAccount read the inline environment values in
their pod templates. Pacto drops those values before anything is stored or
served.

To group inferred services and name their repositories, it also lists Argo CD
Applications and Flux Kustomizations, HelmReleases and GitRepositories, with
`list` only. Listing Applications, HelmReleases and Kustomizations lets the
dashboard and operator ServiceAccounts read their inline Helm values
(`spec.source.helm.values`, `spec.values`, `spec.postBuild.substitute`), which
can hold credentials. Pacto decodes only the source and status fields it names.

Without `registry`, a namespace-list install cannot read the `kube-system`
namespace, so it reports no cluster id. The live source keeps its context name, and a snapshot
with several clusters marks it `CLUSTER_UNKNOWN`.

## What detection reads and writes

[Detection](detection.md) adds three rules to the
operator and none to the dashboard:

| Rule | Where | Why |
|---|---|---|
| `nodes` `list` | Cluster-wide ClusterRole only | Cloud from `providerID` and node kernels |
| `configmaps` `create`, plus `get` and `patch` on `<fullname>-detected` only | Release namespace Role | The one ConfigMap detection writes |
| `deployments` `get` on `<fullname>` only | Release namespace Role | The operator Deployment that owns that ConfigMap |

Kubernetes cannot scope `create` by name, so it stays unnamed. The
leader-election Role grants Leases only, so this is the operator's one
ConfigMap write in the release namespace. Pods,
HTTPRoutes, Ingresses and Services come from reads the operator already has.
A namespace-list install renders no `nodes` rule, so cloud and nodes read
unknown. With `registry` set, it renders one Role in `kube-system` that may
`get` only the `kube-system` Namespace, because the target snapshot needs the
cluster id ([Choose an installation profile](profiles.md#controls-under-hardened)).

## Registry pushes

With `registry` set, the operator pushes only to `<registry>/pacto-targets`,
the target snapshot, and `<registry>/pacto-inferred`, the
[inferred contracts](../../inferred-contracts.md) of external systems. Its
push guard refuses every other tag, delete and manifest push, so its registry
credential needs push on those two repositories only.

## The delivery namespace

Watching or granting the delivery namespace gives the operator its Secrets:
the recorder key and the push-capable `registryCredentials`. The chart then
refuses an `ed25519.secretName` key unless
`delivery.recorder.signing.allowOperatorReadableKey` is `true`. To move an
existing install to a list, see
[Upgrade](upgrade.md#pull-secrets-outside-granted-namespaces).

## Member requests

With delivery and a `registry`, the chart binds the operator to a separate
ClusterRole, `<release>-member-intake`, so the operator ClusterRole stays the
same with delivery on and a member that watches a namespace list still takes
requests. It always lets the operator `get`, `list` and `watch` Stages, to
name the Stages that record a stream in its join block. While
`delivery.remote.enabled` is not `false` and the profile is not hardened, it
also grants:

- `create`, `get`, `list` and `watch` on Operations, to raise the promote a
  [member request](delivery-members.md#deliver-through-a-member) asks for and
  follow it to its outcome. Admission holds `spec.actor` to the operator.
- `create` on SubjectAccessReviews, to check that the person who signed may
  create Operations in the Stage's namespace.
- `get` on the `pacto-delivery-identities` ConfigMap only, for the approver
  signers a request must verify under.

The recorder's public key is copied into `<release>-member-recorder-key`, a
ConfigMap in the release namespace that the operator mounts, so the operator
needs no read in the delivery namespace to advertise it. Set
`delivery.remote.enabled=false` to drop the Operation, review and ConfigMap
grants.
