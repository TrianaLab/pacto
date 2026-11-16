---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

The operator reads no Secret across the cluster. Its ClusterRole drops
`secrets`, and it reads pull secrets and the Secrets behind contract
configurations only in the release namespace, in each watched namespace and
where the new `controller.secretGrants` names them as `namespace/name` (one Role
limited to each named Secret). A Secret-backed configuration it may not read
reads `OBSERVATION_UNSUPPORTED`, and the finding names the grant to add; the
collection does not fail. A pull secret outside every grant sets
`ContractValid=Unknown` with reason `PullSecretNotGranted`. The operator no
longer watches Secrets and reads a rotated pull secret within one check
interval.

`controller.watchNamespaces` takes a list of namespaces and the operator takes
`--watch-namespaces`. With a list, the chart renders Roles and RoleBindings in
each namespace and no ClusterRole, unless the node agent or the dashboard's
delivery views need one. `controller.watchNamespace` and `--watch-namespace`
still work as deprecated aliases that join the list, also in an upgrade that
reuses the earlier release's values. Moving a cluster-wide install to a list leaves the
`pacto-dashboard` ClusterRole and binding behind: the upgrade notes and a
`StaleClusterRBAC` Warning event name the `kubectl delete` command. The chart
refuses an `ed25519.secretName` recorder key without
`allowOperatorReadableKey` whenever a grant reaches the delivery namespace.

`--namespace` on `pacto fleet`, `pacto dashboard` and `pacto mcp` and
`PACTO_DASHBOARD_NAMESPACE` accept a comma-separated list; one value keeps
working. A source that reads a list is always `partial`, names its scope in a
`K8S_NAMESPACE_SCOPED` limitation and in `namespaces` on its source state, and
names a namespace it could not read in `K8S_NAMESPACE_UNREADABLE`. A restricted
view never names a watched namespace the reader may not read. The dashboard
Overview says which namespaces the fleet watches. `--review=local` (or
`PACTO_DASHBOARD_REVIEW=local`) reviews people with LocalSubjectAccessReviews
in the watched namespaces only; the managed dashboard of a namespace-list
install without delivery uses it.

Upgrade: list each pull secret outside the release namespace in
`controller.secretGrants`, or add its namespace to `controller.watchNamespaces`,
before upgrading.
