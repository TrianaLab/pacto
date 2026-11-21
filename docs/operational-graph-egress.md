# See calls that leave the cluster

Pacto turns the calls your workloads make to vendors and legacy systems into
edges of the operational graph, including calls that no configuration, trace
or contract names. It reads what the cluster already records: the node agent,
a service mesh, Cilium and an Istio egress gateway. Each one is optional, and
without any of them every answer reads as before.

## The sources

| Source | What it needs | Provenance | Row |
|---|---|---|---|
| Node agent | `nodeAgent.enabled: true` ([runtime conformance](integrations/kubernetes/runtime-conformance.md)) | observed | `kernel-egress` |
| Istio and Linkerd telemetry | the mesh and the cluster's Prometheus ([mesh calls](operational-graph-mesh.md)) | observed | `istio`, `linkerd` |
| Cilium Hubble flows | Hubble Relay, with DNS visibility for names | observed | `hubble` |
| Istio egress gateway logs | `controller.egressGatewayLogs=on` and gateway access logs | observed | `egress-gateway` |
| Istio `ServiceEntry` hosts | the `networking.istio.io/v1` API | inferred | `istio-egress` |
| Cilium `toFQDNs` rules | the `cilium.io/v2` API | inferred | `cilium-fqdn` |

Every source shows as a row in [Detected providers](operational-graph-providers.md),
with `external egress` among its uses:

```text
GET /api/sources  (egress rows)
kernel-egress   enabled   node agent on 3/4 nodes           coverage partial
istio-egress    enabled   ServiceEntry CRD present          coverage complete
cilium-fqdn     disabled  no CiliumNetworkPolicy CRD        coverage unavailable
egress-gateway  enabled   1 Istio egress gateway found; logs: auto   coverage partial
```

`GET /api/sources` marks these rows `egress: true` with a `coverage` of
`complete`, `partial` or `unavailable`. They read your restricted view, and
the dashboard leaves them out of its data source count.

## From a call to an External

Each observed call becomes an edge from the calling service to the host it
reached. The [external catalog](external-catalog-reference.md#matching)
classifies the host, so `api.stripe.com` joins the External `stripe`. A host
no entry matches joins `host/<registrable domain>`, such as
`host/corp-legacy.com`, and the graph keeps at most
[200 Externals](external-catalog-reference.md#the-cap).

A Cilium `toFQDNs` rule and an Istio `ServiceEntry` state intent. Their hosts
show as inferred uses on the External page, and an inferred use never clears a
gate. Neither proves that a call happened or that it is safe.

## The node agent

The node agent reads the DNS answers its pods receive, and only those: UDP
datagrams from port 53, at most 512 bytes each. It names a connection only
from the answers its own pod received, and only when every name for that
address shares one registrable domain. A shared CDN address therefore stays
unnamed. The operator reads each pod from the node object of the node it runs
on, so one node never speaks for another node's pod.

The operator writes what it found to the `Pacto` status, at most 32 Externals,
one per identity:

```yaml
observedEgress:
  - identity: stripe
    class: vendor-api
    host: api.stripe.com
    port: 443
    source: ebpf
    firstSeen: "2026-11-21T09:12:44Z"
observedEgressCoverage:
  state: partial
  reason: node-without-agent
  nodes: 4
  nodesWithAgent: 3
```

Kernel egress covers workloads that a `Pacto` targets. A mesh or Hubble covers
the rest.

## Coverage

Each source reports its own coverage and never claims more than it read.
While the node agent's egress coverage of a service is not complete, every
answer about Externals turns partial with `EXTERNALS_UNREPORTED`, and the rest
of the snapshot stays complete. The node reasons (`node-without-agent`,
`stale-node` and the others) are those of
[caller coverage](integrations/kubernetes/runtime-conformance.md). The egress
reasons are:

| Reason | Meaning |
|---|---|
| `dns-unavailable` | a node agent reads no DNS answers, or is too old to report them |
| `ambiguous-peers` | an address answers for more than one domain |
| `unnamed-public` | a call reached a public address with no DNS answer |
| `unnamed-private-peers` | a call reached a private address outside every node's pod CIDR |
| `egress-host-unclassified` | a call reached a host whose name gives no identity, such as `legacy-db` |
| `services-unreadable` | the operator could not tell in-cluster addresses apart |
| `no-running-pods` | the workload runs no pod to watch |
| `truncated` | more than 32 Externals |
| `gateway-access-log-off` | an egress gateway wrote no access log line in the window |
| `gateway-log-format` | more than half of the log lines could not be read |
| `gateway-log-unreadable` | the operator could not read a gateway log |
| `cilium-unreadable` | the Cilium policies could not be read |
| `fqdn-pattern-unbounded` | a `toFQDNs` pattern such as `*` names no registrable domain |

Legacy systems are often reached by private IP with no lookup, so an unnamed
private address outside every node's pod CIDR lowers coverage.

## Egress gateway logs

Reading pod logs is a privilege, so a person turns it on.
`controller.egressGatewayLogs` takes `auto` (the default), `on` or `"off"`.
With `auto` the operator reads no log, and the `egress-gateway` row is partial
with a hint that sets the value. To turn it on:

```bash
helm upgrade <release> <chart> --reset-then-reuse-values --set controller.egressGatewayLogs=on
```

With `on` the chart grants `pods/log get` and `pods list` in a Role in
`istio-system`, never in a ClusterRole. The operator trusts only pods labelled
`istio=egressgateway` in `istio-system` that a Deployment there owns. Once a
minute it reads the last 2000 lines, at most 1 MiB, of each gateway's
`istio-proxy` container. A line counts when its downstream address is the IP
of a target pod and it was logged after that pod started. Istio writes no
access log by default.

## Mesh egress

The mesh source reads calls that leave the mesh beside the calls inside it:

- **Istio**: series whose destination workload is `unknown` or an egress
  gateway in `istio-system`, named by a dotted `destination_service_name`.
  Calls through `PassthroughCluster` name no host and make the row partial.
- **Linkerd**: outbound series with no destination workload, named by their
  `authority`.
- **Hubble**: flows to the `world` identity, named by their first
  `destination_names` entry. Flows without names make the row partial.

Each mesh reads at most the 2000 busiest calls to external hosts per window;
beyond that the row carries `EGRESS_SERIES_CAP`.

## What stays unnamed

DNS over TCP, TLS or HTTPS gives Pacto no name, and neither does an egress
gateway other than Istio's. Those calls show as `unnamed-public` or
`unnamed-private-peers`, or not at all.
