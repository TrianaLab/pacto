---
"@pacto/k8s-module": minor
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
capabilities, so the operator namespace must admit privileged
pods. The operator merges the evidence into interface and dependency
observations: a ready pod with nothing listening on a TCP port becomes
`INTERFACE_ABSENT`, the latest connection success or failure within the
staleness bound decides dependency reachability (never against a provider with
no ready endpoint) and a stale or failed node reads `Unknown`, never a
violation. The agent's Role is `get`, `create` and `patch` on its own objects,
the DaemonSet runs on Linux nodes only and the operator deletes the object of a
node that no longer exists.

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
