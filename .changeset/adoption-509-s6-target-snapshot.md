---
"@pacto/core": minor
"@pacto/k8s-module": minor
"@pacto/demo-compose": minor
---

`pacto impact` and `pacto ci` find the consumers that run without cluster
credentials. From the change's `oci://` ref they try
`<host>/<first path segment>/pacto-targets`, then `<host>/pacto-targets`, read
every `cluster-<uid>` snapshot of the first that exists and print a `Registry:`
line and one `Targets:` line per snapshot, pinned by digest. `--target-snapshot`
(repeatable) names a repository or pins cluster tags or digests, and `off`
reads none; `--delivery-trust` or `$PACTO_DELIVERY_TRUST` names the trust
config. A snapshot counts only when a key whose `streams` name its cluster's
UID verified it and its `asOf` is less than 15 minutes old; otherwise its
source reads `partial`, `stale` or `unavailable` with one of
`TARGET_SNAPSHOT_UNVERIFIED`, `TARGET_SNAPSHOT_UNSCOPED`,
`TARGET_SNAPSHOT_STALE`, `TARGET_SNAPSHOT_INVALID` or `TARGET_SNAPSHOT_BOUND`,
and a `BREAKING` change with consumers never clears. No `pacto-targets`
repository leaves today's answer and exit code. `--root` in the CI recipe is
now optional, for consumers that run nowhere yet.

A snapshot becomes up to three fleet sources per cluster: `target-snapshot`
for its Pacto resources, `kubernetes-workloads` for its inferred services,
which never count as consumers, and `evidence-http` for the targets its
Evidence Server accepted. Source states gain `ref`, `signer`, `publisher` and
`cluster`, and `impact` answers gain `registry` and `targetSnapshots`, also on
the `pacto_impact` MCP tool over stdio. `pacto mcp` over HTTP refuses
`--target-snapshot`. `pacto delivery targets publish` signs and pushes a
snapshot document with a PEM key or a `pacto evidence keygen` seed.

The operator publishes the cluster's signed snapshot to
`<registry>/pacto-targets:cluster-<uid>` every 5 minutes when the chart's new
top-level `registry` value is set: the cluster's Pactos, the workloads no
Pacto targets (without env values or probes) and, with `evidence.enabled`, the
Evidence Server's targets, each with its own `asOf` and completeness. Give
`pacto-targets` its own read ACL: pull on it reads every namespace's Pacto
status and workload shape.

The Compose demo runs a `targets` service that publishes a signed snapshot of
the demo cluster to `registry:5000/demo/pacto-targets` every 5 minutes.
