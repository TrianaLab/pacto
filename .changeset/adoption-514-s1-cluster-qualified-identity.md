---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Every target, caller and record names the cluster it is in. A snapshot that
reads a cluster now lists it in `clusters`, and each target carries `cluster`,
`clusterName`, `clusterNameSource`, `clusterNameReportedBy` and
`clusterRelation`. The live Kubernetes source's id is the cluster UID (its
`kube-system` namespace UID) when it can get `namespaces/kube-system`, and the
context name otherwise. The display name comes from a node label that names the
cluster, then the context name, then the first 8 characters of the UID, and its
source is always shown.

Target keys and `k8s:` callers gain a leading cluster component only when the
snapshot holds two or more clusters, so single-cluster keys, hrefs, callers and
MCP answers keep their form. A 3-part key keeps resolving while one cluster
matches it; when several do, the dashboard API answers `409 Conflict` with the
candidate keys, the dashboard offers a chooser and the CLI and MCP print the
candidates. An old dashboard link redirects to the canonical one. Two targets
in two clusters never merge. A target with no cluster id in a multi-cluster
snapshot raises `CLUSTER_UNKNOWN` and makes its source partial.

A `--target-state` file takes an optional `cluster` block with `id` and `name`.
`pacto evidence serve` takes `--cluster-id`, `--cluster-name` and
`--cluster-name-source`; each accepted record carries the cluster that accepted
it, and the targets endpoint answers `pacto.dev/evidence-source/v4` when it
carries a cluster. Two evidence sources that report one record with two
clusters raise `CLUSTER_CONFLICT`.

The operator reads its cluster identity at start and passes it to the Evidence
Server it runs. The operator ClusterRole and the managed dashboard ClusterRole
gain get on `namespaces` for `kube-system` only.
