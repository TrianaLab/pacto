# Clusters and target keys

This page describes how the [operational graph](operational-graph.md) keys
targets when it reads more than one cluster, how old keys keep resolving and
where each cluster name comes from. The live Kubernetes source, a
[target-state file](operational-graph-reference.md#what-a-target-state-fixture-looks-like)
with a `cluster` block and an [Evidence Server](evidence.md#clusters) each
report a cluster.

## Key forms

A target key names one target as `<scope>/<kind>/<name>`. The cluster
component appears only when the snapshot holds more than one cluster: then each
target with a known cluster gains a leading component, the UID of that
cluster's `kube-system` namespace. Every component is escaped the same way. A
snapshot that reads a cluster lists it in `clusters`, with the sources that
reported it.

| Snapshot | Key of the `payments` Deployment |
|---|---|
| Zero or one cluster | `payments/kubernetes/payments` |
| Two or more clusters | `9f1c4e2a-…/payments/kubernetes/payments` |
| Two or more clusters, target with no known cluster | `_/payments/kubernetes/payments` |

The `_` component never names a real cluster, so a target with no known
cluster keeps a key of its own.

## Resolving a key

| You ask for | Answer |
|---|---|
| A 3-part key with zero or one cluster | That target |
| A 3-part key that one cluster matches | That target, under its 4-part key |
| A 3-part key that one cluster matches while a source is unavailable or partial | That one candidate to choose; the missing source may hold another |
| A 3-part key that several clusters match | The candidates; nothing is guessed |
| A 4-part key with zero or one cluster | The 3-part target in that cluster |
| A 4-part key whose target has no known cluster | That target as the one candidate, with the reason |
| A `_` key with zero or one cluster | The 3-part target with no known cluster |

On an ambiguous key the dashboard API answers `409 Conflict` with one
`candidates` detail per target (the key as `value`, its label as `message`),
and the dashboard page lets you choose. Name ambiguity keeps `422`. The CLI
and MCP print the candidates; the CLI exits 1:

```text
ambiguous target key: this target key names targets in more than one cluster; choose one; candidates:
  us-prod / payments / payments   77ad01b3-…/payments/kubernetes/payments
  eu-prod / payments / payments   9f1c4e2a-…/payments/kubernetes/payments
```

A candidate list holds only targets your view may see, and every candidate
key opens its own target.

## Cluster names

A target with a cluster component is shown as `eu-prod / payments / payments`.
The name is display only, and `clusterNameSource` says where it came from:

| `clusterNameSource` | Name |
|---|---|
| `node-label` | A node label that holds the cluster name, such as `alpha.eksctl.io/cluster-name` |
| `context` | The kubeconfig context name |
| `file` | The `cluster.name` of a target-state file |
| `uid` | The first 8 characters of the UID |

A name the live source read always wins over a name another source reported,
and a reported name carries `clusterNameReportedBy`. A reported name is at most
63 bytes; a name the live source read is at most 253, so an EKS ARN context
name fits. When two clusters resolve to the same name, each name gains the
first 8 characters of its UID, such as `eu-prod (9f1c4e2a)`. Renaming a cluster keeps
its UID; a rebuilt cluster has a new UID and so new keys.

## Targets with no cluster

`CLUSTER_UNKNOWN` names a source whose targets carry no cluster id. With
several clusters it makes that source partial. With one cluster it does so
only when such a target merged into a clustered one; otherwise it is
informational. With no known cluster it never appears.

## Migrating stored keys

A stored 3-part key keeps working while one cluster matches it, and an old
dashboard link redirects to the canonical one. Store the key an answer
returns. A 4-part key never becomes ambiguous.
