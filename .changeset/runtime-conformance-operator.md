---
"@pacto/k8s-module": major
---

Add an opt-in eBPF node agent and the runtime checks it and the Kubernetes API
make possible.

`nodeAgent.enabled` deploys a DaemonSet (`pacto agent --sink kubernetes` from
the pacto runtime image the operator already runs the Evidence Server from;
`--node-agent-image` overrides it) that
watches TCP socket state through one BTF tracepoint and
publishes each node's listeners and connections as a `NodeRuntimeObservation`
object, a new CRD. It reads no payloads, files or Secrets and opens no port. It
needs Linux 5.8 or later with BTF, `hostPID`, the node's `/sys/fs/cgroup`
mounted read-only, root and the `BPF`, `PERFMON` and `SYS_PTRACE`
capabilities, so it runs in its own namespace (`nodeAgent.namespace`, default
`pacto-node-agent`), which the chart creates with the privileged Pod Security
level; the release namespace keeps whatever level you set. Each agent renews a
`coordination.k8s.io` Lease named after its node every publish interval and
rewrites its `NodeRuntimeObservation` only when the view changes (or every tenth
interval); the operator reads the node's heartbeat from the Lease. Where the
API serves `ValidatingAdmissionPolicy` (Kubernetes 1.30 or later), the chart
binds each agent's writes to the node named in its token, so one agent cannot
speak for another node. The operator merges the evidence into interface and dependency
observations: a ready pod with nothing listening on a TCP port becomes
`INTERFACE_ABSENT`, the latest connection success or failure within the
staleness bound decides dependency reachability (never against a provider with
no ready endpoint) and a stale or failed node reads `Unknown`, never a
violation. The agent's Role is `get`, `create` and `patch` on its observations
and `get`, `create`, `patch` and `update` on its Leases, the DaemonSet runs on
Linux nodes only and the operator deletes the objects of a node that no longer
exists.

Findings carry `evidenceRefs[].collector` and `detectedAt`;
`status.evaluationCoverage.unverified` lists facets with no usable fact.

**Behaviour change:** the complementary checks run with or without the node
agent, because none of them depends on it: the interface protocol (with
`controller.enableProbing`), the interface exposure, the provider version for
`dependencies[].compatibility` (which also picks the target when several
providers match the ref) and the persistence scope (read-only claims ignored).
Exposure is `external` only on positive evidence of a public address: a public
`externalIPs` entry, or a `LoadBalancer` Service without an internal load
balancer annotation or an Ingress whose status carries a public IP or
hostname. A `NodePort` Service or an HTTPRoute leaves it unknown. An interface
with no `visibility` is `internal`, so one behind such a public address now
reports `INTERFACE_VISIBILITY_MISMATCH`. Declare `visibility: public` for it.

Fixes: interface availability compared the Service port with the EndpointSlice
port, which carries the target port, so a Service with `port` different from
`targetPort` read absent; `oci://` dependencies never matched a sibling Pacto
and read `Unsupported`; passive health ignored the declared path. The chart now
exposes `controller.enableProbing` and `controller.enableMetricsObservation`,
grants `list` and `watch` on ConfigMaps, which the cached reads needed, and
adds read access to pods, Ingresses, HTTPRoutes and `NodeRuntimeObservation`
objects. Cached pods are trimmed to the fields the operator reads.

The kernel merge and the probes now come from core `pkg/runtimefacts` and
`pkg/probe`, so the same verdicts hold on Kubernetes and on any host.

The operator uses evidence types from this release's core, so ship it in the
same release as `@pacto/core`: the core tag is created first and the operator
`go.mod` must pin it.

**Breaking changes and migration (operator v6)**

- The node agent moved to a dedicated namespace. Set `nodeAgent.namespace` if
  `pacto-node-agent` does not suit you; it must not be the release namespace and
  must not already exist outside this release. Remove the
  `pod-security.kubernetes.io/enforce=privileged` label you may have put on the
  release namespace for the agent. The operator deletes the agent it ran in the
  release namespace (DaemonSet, RBAC and observations) at its first sync. A
  private `imagePullSecrets` entry must also exist in the agent namespace.
- `--kernel-evidence-namespace` (and `controller.kernelEvidence.namespace`)
  now defaults to the agent namespace, not the operator's. New flag:
  `--node-agent-namespace`.
- The managed components' lifecycle permissions moved out of the ClusterRole:
  a `Role` in the release namespace (dashboard and Evidence Server, plus the
  teardown of the old agent) and a `Role` in the agent namespace (the agent's
  DaemonSet, RBAC, observations and Leases). The ClusterRole keeps the reads,
  the dashboard's ClusterRole and binding, node reads, Lease reads and a by-name
  read and relabel of the agent namespace. The operator no longer creates
  namespaces at all, and no longer needs `namespaces` list or watch. An operator
  installed without the chart needs the new Roles (`config/rbac/role.yaml`
  generates them).
- Objects the operator creates for the agent no longer carry an owner
  reference to the operator Deployment: it lives in another namespace, and
  Kubernetes garbage-collects a dependent whose owner is in another namespace.
