---
"@pacto/core": minor
---

A cluster that runs Istio, Linkerd or Cilium Hubble now gives the operational
graph runtime edges from the first refresh, with no node agent and no chart
value. Every `--k8s` read finds the mesh through API discovery and reads the
calls of the last hour that the mesh already records: Istio and Linkerd from
the cluster's Prometheus, Hubble from Hubble Relay. A call between two declared
services is observed, a call with an inferred end is inferred and never clears
a gate, and an Istio workload name resolves to the one inferred service of that
name. Each mesh is one more Detected providers row in Data sources,
`pacto fleet providers` and `pacto_fleet_status`. Uncovered namespaces, a
missing or silent Prometheus, an unreachable or TLS-only Relay and calls Pacto
could not attribute make the row partial. Pacto asks only a Prometheus or Relay
it can trust, reads at most once every 5 minutes within one 15-second deadline
and, outside the cluster, reaches Prometheus through the API server proxy with
your own credentials.
