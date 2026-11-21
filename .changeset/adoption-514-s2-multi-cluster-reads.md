---
"@pacto/core": minor
---

One Pacto reads several clusters: pacto fleet, tui, impact, stats, mcp and
dashboard take a repeatable --context, each kubeconfig context read with its
own credentials and each cluster read once. On pacto dashboard the named
contexts replace the current context outside a pod, and people sign in
against the first one. Each signed-in person is reviewed in each cluster by
that cluster's own reviewer, so a grant in one cluster never shows the same
namespace in another; ServiceAccount and node identities are reviewed only in
the sign-in cluster. A failed or timed-out review now hides that cluster and
marks the answer partial with a CLUSTER_HIDDEN limitation instead of failing
the request, single-cluster dashboards included. An unreachable cluster reads
stale in the dashboard and MCP server and unavailable in one-shot commands.
Every fleet answer carries meta.clusters; the dashboard API takes ?cluster=,
pacto fleet entities takes --cluster, the MCP tool pacto_fleet_entities takes
a cluster argument and the Services and Targets lists gain a Cluster filter.
The statistics answer counts operation records of other clusters in
hidden.otherClusters and leaves them out of every number.
