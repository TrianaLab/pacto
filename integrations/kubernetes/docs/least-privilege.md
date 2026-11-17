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
kubectl get clusterrole | grep pacto   # prints nothing
```

The chart then renders a Role and a RoleBinding in each listed namespace for
the operator and the dashboard, and no ClusterRole. Two features still need
one: the node agent and the dashboard with delivery on, whose delivery views
read across the cluster. The dashboard Overview starts with "Watching 2
namespaces (ledger and payments)." Every fleet answer is `partial`, and a
service in any other namespace is unknown.

In both install modes the dashboard lists Deployments, StatefulSets,
DaemonSets and CronJobs, with `list` only, to show them as inferred services.
Listing them lets its ServiceAccount read the inline environment values in
their pod templates. Pacto drops those values before anything is stored or
served.

A namespace-list install cannot read the `kube-system` namespace, so it
reports no cluster id. The live source keeps its context name, and a snapshot
with several clusters marks it `CLUSTER_UNKNOWN`.

## The delivery namespace

Watching or granting the delivery namespace gives the operator its Secrets:
the recorder key and the push-capable `registryCredentials`. The chart then
refuses an `ed25519.secretName` key unless
`delivery.recorder.signing.allowOperatorReadableKey` is `true`. To move an
existing install to a list, see
[Upgrade](upgrade.md#pull-secrets-outside-granted-namespaces).
