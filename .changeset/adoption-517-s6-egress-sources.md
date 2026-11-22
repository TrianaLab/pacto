---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

See the vendors and legacy systems your workloads call, including calls that
no configuration, trace or contract names. The node agent now reads the DNS
answers its pods receive (UDP from port 53, at most 512 bytes) on the same eBPF
attach and with no new capability, and names each connection `host` from its
own workload's answers; a shared CDN address stays unnamed. To make room for
names in the node status, the node-wide connection cap drops from 4096 to 3072.
The operator writes the named calls that leave the cluster to
`status.observedEgress` (at most 32 per Pacto) with
`status.observedEgressCoverage`, which counts the nodes with an agent and says
why it is partial; the Pacto status format becomes 4. Istio, Linkerd and
Hubble telemetry add calls to external hosts from the mesh source. With
`controller.egressGatewayLogs=on` the operator reads the access log tail of
the Istio egress gateways in `istio-system`, through a Role there that grants
`pods/log get`; the default `auto` reads nothing and proposes the value.
Cilium `toFQDNs` rules that select a workload become inferred `ciliumFQDN`
external uses, which never clear a gate. Every call joins the External the
catalog names, observed, and partial egress coverage turns external answers
partial. The `kernel-egress`, `istio-egress`, `cilium-fqdn` and
`egress-gateway` rows show in Detected providers, `pacto fleet providers`,
`pacto_fleet_status` and `GET /api/sources`.
