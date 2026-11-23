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
same claim mapping. A [member](member-clusters.md#identity-models) with
another issuer reviews people under an approved group mapping.

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

A cluster can also join a hub as a [member](glossary.md#member) through the
registry alone, with no kubeconfig of it, and link out to the hub so that
each member reviews signed-in people with its own RBAC. See
[Member clusters](member-clusters.md).

## Operation records

Delivery runs in one cluster. The Metrics page leaves out the operation
records of a service you may read whose Stage is in another cluster, and
counts them: `12 operation records from other clusters are not shown`. The
statistics answer carries the count as `hidden.otherClusters`. When the
dashboard cannot read the sign-in cluster's identity, every record that names
a cluster is hidden, with a `HOME_CLUSTER_UNKNOWN` limitation that counts them.
