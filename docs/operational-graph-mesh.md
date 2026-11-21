# Calls a service mesh records

Pacto joins the calls Istio, Linkerd or Cilium Hubble already record to the
operational graph, with no node agent, no collector and no chart value.

## When Pacto reads a mesh

Every `--k8s` source of `pacto fleet`, `pacto impact`, `pacto mcp --fleet` and
`pacto dashboard` comes with a mesh source, `<cluster>-mesh`, of kind `mesh`.
It uses API discovery to find a mesh:

| Mesh | Found when the cluster serves | Calls read from |
|---|---|---|
| Istio | `networking.istio.io` | The cluster's Prometheus |
| Linkerd | `policy.linkerd.io` or `linkerd.io` | The cluster's Prometheus |
| Cilium Hubble | `cilium.io` | Hubble Relay |

When the cluster serves none of these groups, the source makes no other call
and reports no row. Otherwise it lists Services and Namespaces and reads the calls of the last hour. It reads no
Pod, Secret or custom resource and writes nothing. The only requests it sends
outside the Kubernetes API are `GET /api/v1/query` to Prometheus and the
`ServerStatus` and `GetFlows` calls to Hubble Relay.

Each mesh found is one more row in
[Detected providers](operational-graph-providers.md), with the id `istio`,
`linkerd` or `hubble` and `observed edges` as its use:

```text
$ pacto fleet providers --k8s
PROVIDER  EVIDENCE                                                USED FOR
Istio     serves networking.istio.io; calls read from Prometheus  observed edges (partial: 3 namespaces without sidecars)
```

| Evidence ends with | Meaning |
|---|---|
| `calls read from Prometheus`, `flows read from Hubble Relay` | Pacto read the calls |
| `Prometheus found, calls not read`, `Hubble Relay found, flows not read` | The server failed to answer; a limitation says why |
| `no Prometheus found`, `no Hubble Relay found` | No server Pacto trusts was found |
| `Services not read` | A Services list failed, so Pacto did not look for a server |

The row's objects name the mesh's control-plane Services, the Prometheus or
Relay Service Pacto read and the namespaces the mesh does not cover. The
evidence and the limitations never name a namespace or a Service, so a
[restricted view](operational-graph-providers.md#restricted-views) hides
exactly the objects you cannot read.

## What Pacto reads

For Istio, Pacto asks Prometheus for `increase(istio_requests_total[1h])`
summed by reporter, cluster, workload and namespace of both ends, or
`istio_tcp_connections_opened_total` for TCP traffic. A call both sidecars
reported counts once. For Linkerd it asks for the outbound
`increase(response_total[1h])`, or `tcp_open_total`, summed by the workload
labels of both ends. For Hubble it reads Relay's status and at most the newest
10000 flows of the last hour, keeping forwarded egress flows with a workload
on both ends.

Pacto skips calls between two clusters, and an answer holding several clusters
reads partial with no edge. Pacto tells clusters apart by a series' `cluster`
label, else by Istio's own cluster labels. Linkerd labels only calls to a
mirrored service, so without a `cluster` label Pacto cannot spot its calls of
another cluster. A Hubble flow that does not say whether it replies is no edge.

Each call pair becomes an observed edge with its call count, first seen one
hour before the read and last seen at the read. For Hubble, first and last
seen are the times of the earliest and latest flow.

## How names map

Linkerd and Hubble name each end with its namespace, kind and workload name,
which is the key of the [inferred service](operational-graph-inferred.md) that
workload already is. Istio names only the namespace and the workload name, so
Pacto looks the name up among the workloads it knows. One workload of that name
resolves to its service, a declared service whose Pacto target runs as that
workload included. Two workloads of different kinds with that name are
`ambiguous: more than one workload of this name`, and no workload leaves the
end unresolved, as does a cluster whose workload list is incomplete.

Mesh calls join inferred services like every runtime call; see
[Calls with no contract on one end](operational-graph-inferred.md#calls-with-no-contract-on-one-end).

## Coverage

Pacto expects every namespace that holds a Service to be covered, except
`kube-system`, `kube-public`, `kube-node-lease` and the mesh's own namespaces.
Istio covers a namespace labelled `istio-injection=enabled`,
`istio.io/dataplane-mode=ambient` or with an `istio.io/rev` label and no
`istio-injection` label, which wins over it. Linkerd
covers a namespace annotated `linkerd.io/inject=enabled`. Pacto reads no Pod,
so a workload injected only by its own pod template in an unlabelled namespace
counts as uncovered. Each uncovered namespace is an object on the row, and the
row reads `3 namespaces without sidecars`.

Hubble covers each node Cilium runs on. A node Relay cannot reach reads
`1 node not reachable through Hubble Relay`. Each node keeps its flows in a
ring buffer, 4095 flows by default, so on a busy node Relay holds minutes of
flows rather than the hour. When any node's earliest flow is more than a minute
later than the start of the hour, the row says how much it read, such as
`Hubble Relay kept only the last 12 minutes of flows`.

## Which Prometheus and Relay Pacto trusts

A tenant who can create a Service must not be able to feed Pacto calls. Pacto
never asks a Service of type `ExternalName` or one with no selector, since
either can point anywhere. It looks for Prometheus Services in this order and
asks at most three:

1. For Linkerd only, the viz extension's Prometheus
   (`linkerd.io/extension=viz`, `component=prometheus`).
2. `operated-prometheus=true`, the Prometheus Operator.
3. `app.kubernetes.io/name=prometheus`.
4. `app=prometheus`.

A candidate in a namespace of the mesh's control plane is trusted. Any other
candidate is trusted only when every Service of its label class sits in its
namespace, and is asked only when no trusted candidate exists. When a class has
candidates in two or more other namespaces, Pacto asks none of them. The first
answer that holds at least one series is used.

Pacto reads Hubble Relay only from a Service labelled `k8s-app=hubble-relay`,
or named `hubble-relay`, in `kube-system` or in the namespace of Cilium's own
Services.

Anyone who can create a Service can copy a label, so a control-plane or
Cilium label counts only when every Service carrying it sits in one namespace.
A copy elsewhere marks neither namespace and lists no control-plane Service:
the mesh may go unread, but Pacto never reads a tenant's server. When a Services
list fails, no Service is trusted for being alone in it. A scoped installation
that cannot see the real control plane still trusts a lone copy.

An installation that watches some namespaces reads only those. It counts any
call that reaches another namespace without naming it.

## How often Pacto reads

Each process reads a mesh at most once every 5 minutes, however often the
dashboard refreshes. Each mesh row's `asOf` and the source's `observedAt` stay the time of
the real read. Each query has a
10-second timeout and the whole read has one 15-second deadline, after which
Pacto asks no further candidate. A Prometheus answer is limited to 16 MiB and
the 5000 busiest call pairs.

## Partial reasons

A mesh Pacto found but could not read in full makes its row `partial`, never
complete and empty. A series with no call in the hour is a read with no edge.
Pacto reports the source `unavailable` only when API discovery fails.

| Code | Messages |
|---|---|
| `MESH_TELEMETRY_UNREAD` | `no Prometheus found in the namespaces this installation reads`; `several Prometheus servers could record this mesh; Pacto cannot tell which one does`; `the Prometheus Pacto found holds no istio_requests_total series for the last hour, so it does not record this mesh's calls`; `the Prometheus Pacto found refused the query (HTTP 503)`, `timed out`, `answered more than 16 MiB`, `answered with an unreadable body`, `is not reachable from here` or `is not reachable from here and you may not proxy to it`; `this Prometheus holds calls of several clusters; Pacto cannot tell which is this one`; `only the 5000 busiest call pairs were read`; `Services could not be read in every namespace, so Pacto did not look for the Prometheus that records this mesh`; `the mesh's control plane could not be told apart from a copy in another namespace`; `no Hubble Relay found`; `Services could not be read in every namespace, so Pacto may miss Hubble Relay`; `Hubble Relay serves TLS; Pacto reads no Secret for a client certificate, so its flows are not read`; `Hubble Relay is not reachable from here`, `timed out` or `refused the request`; `Hubble Relay served no flow for the last hour`; `Hubble Relay holds flows of several clusters; Pacto cannot tell which is this one`; `only the newest 10000 flows of the last hour were read` |
| `MESH_COVERAGE_PARTIAL` | `3 namespaces without sidecars`; `sidecar coverage is unknown because namespaces could not be read`; `2 nodes not reachable through Hubble Relay`; `Hubble Relay did not say how many nodes it reaches`; `40 flows were lost before Hubble Relay could serve them`; `Hubble Relay kept only the last 12 minutes of flows` |
| `MESH_CALLS_UNATTRIBUTED` | `4 call series had an end Pacto could not name`; `2 call series reach namespaces this installation does not read`; `1 cross-cluster call was not attributed`; `3 flows reach namespaces this installation does not read`; `1 cross-cluster flow was not attributed`; `2 flows had no request direction` |

An end Pacto cannot name is `unknown` in Istio, a workload label left empty in
Linkerd, a Job pod or a peer outside the mesh.

## Why a partial mesh leaves the answer complete

The mesh source is advisory, like [operator detection](operational-graph-reference.md#sources).
It turns on by itself, so were it counted, a cluster with one uninjected
namespace would make every fleet and impact answer partial and every CI impact
gate unverified. Its edges still join the graph. Its silence never counts against a declared
edge and an inferred edge never clears a gate.

Data sources lists the mesh source with its state. While it is not available,
the state bar, the overview's chips and the Incomplete sources tile leave it
out, as does each state filter, and the bar names it.

## Outside the cluster

Inside the cluster Pacto reads Prometheus and Relay directly. Outside it, Pacto
never resolves a cluster address and reaches Prometheus through the API
server's service proxy with your own
kubeconfig credentials, which need `get` on `services/proxy`. Without it the
row reads `is not reachable from here and you may not proxy to it`. Hubble
Relay is read only inside the cluster, so outside it the row reads
`Hubble Relay is not reachable from here`. The dashboard's ServiceAccount is
never granted `services/proxy`.

## What Pacto does not read

| Left out | Why |
|---|---|
| Calls to hosts outside the cluster, such as Istio `ServiceEntry` hosts or Hubble flows to `world` | The mesh source reads calls between workloads only |
| A Hubble Relay that serves only TLS | Its client certificate sits in a Secret Pacto does not read |
| A Prometheus, Thanos or Mimir outside the cluster | Its address would be a chart value |
