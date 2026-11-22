# Multi-cluster

This page describes how one Pacto reads the Pacto resources of several
clusters and how each signed-in person sees only what each cluster's own RBAC
lets them read. Keys and cluster names are covered in
[Clusters and target keys](operational-graph-clusters.md).

## Read several clusters (ad hoc, read-only)

Name each cluster by its kubeconfig context with `--context`, once per
cluster:

```sh
pacto dashboard --context eu-prod --context us-prod
pacto fleet status --context eu-prod --context us-prod
pacto mcp --fleet --context eu-prod --context us-prod
```

`pacto fleet`, `pacto tui`, `pacto impact`, `pacto stats` and `pacto mcp`
take `--context` beside `--k8s`. `--k8s` keeps reading the current context,
and `--k8s --context us-prod` reads both. On `pacto dashboard`, outside a pod,
the named contexts replace the current context: the dashboard neither reads
it nor signs people in against it. Each context is read with its own
credentials, even when Pacto runs in a pod with a kubeconfig it mounts itself.

These reads are ad hoc and read-only. Pacto reads Pacto resources only, never
delivery objects or an internal developer platform, and writes nothing in any
cluster.

### Each cluster is read once

Pacto identifies a cluster by the UID of its `kube-system` namespace. Two
contexts that reach the same UID on the same server and certificate authority
are one cluster, read once through the first context. One UID on two servers,
which happens when a cluster is restored from another cluster's etcd snapshot,
is a `CLUSTER_CONFLICT`: Pacto reads only the first context and marks the
answer partial.

A context whose cluster identity Pacto cannot read is refused when other
clusters are read beside it, with a `CLUSTER_UNKNOWN` limitation that names
the context, so same-named targets of two clusters never merge. A single
context keeps working without an identity.

## The sign-in cluster

The dashboard signs people in against one cluster, the sign-in cluster: the
in-cluster configuration when the dashboard runs in a pod, else the first
`--context`, else the current context. The dashboard prints it at start-up as
`Sign-in cluster:`.

## What each person sees

Pacto asks each cluster's API server, with that context's credentials, a
SubjectAccessReview about the signed-in person. A grant on `prod` in
`eu-prod` shows `prod` in `eu-prod` only, never `prod` in `us-prod`. Each
context's credentials need `create subjectaccessreviews` in their cluster.
Answers are kept for a minute per cluster, person, resource and namespace, so a
removed RoleBinding hides its namespace within that time. A failed review is
never kept.

Every cluster reviews the same person the sign-in cluster signed in. This is
right when every cluster maps people the same way: one OIDC issuer with the
same claim mapping. Clusters with different issuers are not supported yet.

ServiceAccount and node identities exist in one cluster only, so Pacto reviews
a person whose user starts with `system:serviceaccount:` or `system:node:`, or
who is in the `system:serviceaccounts` or `system:nodes` group, in the sign-in
cluster only. Every other cluster is hidden from them as not reviewable.

The HTTP MCP fleet tools and the MCP delivery reads review callers in each
cluster the same way. Their sign-in cluster is always the current context, or
the in-cluster configuration in a pod, since `--context` adds clusters there.
`pacto mcp` prints it at start-up and warns when the fleet does not read it;
add `--k8s` so that ServiceAccount callers see their own cluster.

## Hidden clusters

A cluster a person sees nothing of is named, never silently left out. Each
one adds a `CLUSTER_HIDDEN` limitation and marks the answer partial; the
other clusters' answers stay whole and the request never fails.

| Word | Meaning |
|---|---|
| `no access` | You may read no namespace in that cluster. |
| `review failed` | The review failed or took longer than 5 seconds; the cluster shows again when a review answers. |
| `not reviewable` | Pacto has no way to review you there: no context reaches it, its identity conflicts or you signed in with a ServiceAccount or node identity of another cluster. |

A cluster that stops answering is never read as empty. The dashboard and an
MCP server keep the last good read and show it as `stale`, with its targets.
A one-shot command has no earlier read, so the cluster is `unreachable` and
the answer partial.

## Cluster status and filter

Every fleet answer lists each cluster in `meta.clusters`, with its status,
its access word and how many targets it shows. `pacto fleet status` prints a
`Clusters:` line with each cluster's UID, such as
`Clusters: eu-prod (5b0c...) complete, us-prod (9f1c...) no access`. A context
Pacto refused to read shows why: `conflicting id` or `unidentified`.

List one cluster's targets, and the services and revisions that run there,
with its UID. Inferred services of that cluster are listed too. An id that no
cluster has, such as a cluster's name, answers an empty list with a
`CLUSTER_FILTER_UNKNOWN` limitation:

```sh
pacto fleet entities --kind target --cluster 9f1c4e2a-... --context eu-prod --context us-prod
```

The MCP tool `pacto_fleet_entities` takes the same `cluster` argument and the
dashboard API takes `?cluster=`. On the dashboard, the Services and Targets
lists show a Cluster filter with a line such as
`Showing 1 of 2 clusters (us-prod: no access)`, and Data sources shows each
cluster's status, both only when two or more clusters are known.

## Join a cluster through the registry

A cluster can join a hub as a [member](glossary.md#member) through the
registry alone, with no kubeconfig of it and no network path between the
clusters. The [hub](glossary.md#hub) is any cluster whose dashboard,
`pacto mcp` or `pacto cluster` reads that registry and whose operator lists
approver signers.

### Set up the member and the hub

1. Install the operator in the member with `--set registry=ghcr.io/acme` and
   nothing else. It publishes its signed snapshot to
   `ghcr.io/acme/pacto-targets:cluster-<uid>` with its display name, the
   environments it seems to host and its installation key.
2. On the hub's operator, set `delivery.enabled=true` and
   `delivery.identities.signers`. A hub without signers counts no approval and
   says so on every pending row.
3. For a private registry, add a pull-only Secret to the hub's
   `dashboard.ociSecrets`. Never reuse `registryPullSecret`: it can push.
4. Let the people who approve members `approve` clusterjoins, as users:

```sh
kubectl create clusterrole pacto-cluster-approver --verb=approve --resource=clusterjoins.pacto.trianalab.io
kubectl create clusterrolebinding pacto-cluster-approver --clusterrole=pacto-cluster-approver --user=oidc:alice@acme.com
```

`clusterjoins` is a virtual resource with no CRD. The hub reviews the user an
approval's certificate names with no groups, since a certificate names none,
so a grant to a group never counts. A profile `hardened` cluster can be a
member but never a hub: it renders no delivery and so no approver signers.

### Approve a member

The new cluster shows as pending on Data sources, in
`pacto cluster list --registry ghcr.io/acme` and in the MCP tool
`pacto_cluster_list`. Ask the member's admin for its key id, read in the
member:

```sh
kubectl get configmap pacto-operator-detected -n pacto-operator-system -o jsonpath='{.data.detected\.json}' | jq -r .installation.snapshotKeyID
```

If it matches the pending row, approve the key you checked. `--name` sets the
display name:

```sh
pacto cluster approve 9f1c4e2a --key sha256:<snapshotKeyID> --name eu-prod
```

The command checks your grant, signs you in once in the browser and records an
approval signed as you beside the snapshot. It refuses when the snapshot names
another key by then. The dashboard's Approve button shows this command.

From the next fleet build on, the member's services, targets and workloads
appear, verified under the approved key and scoped to its cluster. People
signed in to the dashboard or an HTTP MCP server see a member hidden and
counted, since it has no live channel yet that could review them there.

The hub checks every approval each time it reads it, so a person who loses the
grant turns their approvals pending. A dashboard installed with a namespace
list cannot review `clusterjoins`, so its approvals stay pending.

### Remove, rebuild and rotate

- `pacto cluster remove 9f1c4e2a` records a revocation signed as you. The
  member's data leaves the next fleet build. Rotate or delete its registry
  credential too.
- A rebuilt cluster has a new UID. It shows pending with "may replace eu-prod"
  when it keeps the name of an approved member.
- A new installation key on a known UID shows pending again, with the key
  that changed, until someone approves the new key.

### The registry boundary

Every member pushes to `pacto-targets`. Give members credentials without
delete, and turn on tag immutability where the registry offers it, such as zot
or Harbor. A record names the key it approves, so a swapped tag never makes a
member, but a member that can delete can remove what others pushed. The hub
reads every `cluster-*` tag within 10 listing pages. It reads approved members
first and up to 32 pending clusters. It counts the rest without reading them
and marks the list partial.

## Operation records

Delivery runs in one cluster. The Metrics page leaves out the operation
records of a service you may read whose Stage is in another cluster, and
counts them: `12 operation records from other clusters are not shown`. The
statistics answer carries the count as `hidden.otherClusters`. When the
dashboard cannot read the sign-in cluster's identity, every record that names
a cluster is hidden, with a `HOME_CLUSTER_UNKNOWN` limitation that counts them.
