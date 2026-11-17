---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Every workload a cluster runs shows up in the fleet as an inferred service,
with its image, ports and probes; inferred services never satisfy a dependency
or clear a gate. A new `kubernetes-workloads` source lists Deployments,
StatefulSets, DaemonSets and CronJobs (`list` only) and reads no environment
variable, Secret or ConfigMap. Service records carry `declaration`
(`declared` or `inferred`); `pacto fleet entities` and `pacto fleet search`
take `--declaration`, the fleet MCP tools take `declaration`, the Overview
counts `inferredServices` apart and the Services list shows inferred services
muted with a filter. An inferred caller of a declared service stays an
uncontracted caller and links to its entity. The dashboard ClusterRole and the
namespace watch Role gain `list` on the four workload kinds, and the operator
ClusterRole gains `daemonsets`.
