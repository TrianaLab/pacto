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
