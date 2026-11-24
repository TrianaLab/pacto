# What Pacto detects

On start and every 5 minutes the operator reads what the cluster provides
and writes it to the `<release>-detected` ConfigMap in its namespace, such as
`pacto-operator-detected`. Each row is inferred and carries its source and
when it was read. Nothing is turned on:
detection changes no argument, rule or feature.

| Row | Read from | Example |
|---|---|---|
| Registry | Running pods, one vote per namespace | `ghcr.io/acme in 9 of 11 namespaces, 87% of workloads` |
| Cloud | Node `providerID` and labels | `GKE on 6 of 6 nodes` |
| Sign-in | API discovery | `Dex serves dex.coreos.com/v1` |
| Exposure | HTTPRoutes and Ingresses, one vote per namespace | `Gateway API with HTTPRoutes in 4 of 5 namespaces` |
| Metrics | API discovery | `Prometheus Operator serves monitoring.coreos.com/v1` |
| Nodes | Node kernels | `kernel 6.1 on 6 of 6 with BTF likely` |
| Istio egress and Cilium FQDN | API discovery; see [Calls that leave the cluster](../../operational-graph-egress.md) | `ServiceEntry CRD present` |
| Egress gateway | Pods labelled `istio=egressgateway` in `istio-system`, only when found | `1 Istio egress gateway found; logs: auto` |
| OTel Collector, Tempo, Jaeger and Datadog | Running pods and Services, only when found; see [Trace backends](../../operational-graph-providers.md#trace-backends) | `query API on port 16686` |
| Backstage | A running Backstage image and a Service on port 7007 in its namespace; see [A detected catalog](../../backstage-catalog.md#a-detected-catalog). With no Backstage image in a watched namespace the row reads `not detected`, partial, because an external Backstage is invisible to the cluster | `Backstage` |

The rows show under Data sources in the dashboard, in `helm upgrade` notes
and in the CLI:

```bash
pacto fleet status --k8s --detected k8s:pacto-operator-system/pacto-operator-detected
```

A registry or exposure row prints a command to review only when one
candidate holds most namespaces and leads every other one. Docker Hub
official images are never proposed as a push target. BTF reads likely,
unlikely or unknown and is never confirmed. A missing or stale detection shows on its own row and never
makes an answer partial.

With `controller.watchNamespaces` the registry row is partial and names the
watched namespaces, and cloud and nodes are unknown. Beyond the ConfigMap it
writes and the operator Deployment that owns it, the only grant detection adds
is `nodes list` in the cluster-wide ClusterRole; see
[Run with least privilege](least-privilege.md#what-detection-reads-and-writes).
