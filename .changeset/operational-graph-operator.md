---
"@pacto/k8s-module": minor
---

Run the operational graph in a cluster: an operator-managed Evidence Server and observed-dependency input for the dashboard.

- **Managed Evidence Server.** Set `evidence.enabled` and the operator reconciles
  a separate Evidence Server Deployment and an internal Service, with optional
  Ingress or Gateway API `HTTPRoute` exposure. It is single-writer, so its replica
  count is fixed at one, and it runs the same runtime image as the managed
  dashboard. `evidence.registry.subjects` names the exact immutable contract
  revisions evidence may be reported against; the registry holding them is the
  durable store, so the chart installs no volume, database or bucket of its own.
- **Observed dependencies.** `dashboard.observation.sources` mounts offline
  OTLP/JSON trace exports read-only into the managed dashboard, so its
  operational graph can reconcile declared dependencies against observed ones.
  Each entry is one named data source with a stable identity, read through a root
  the process cannot follow a symlink out of, never written to and never scanned.
  This is offline input only: no OTLP receiver ships and no collector is deployed.
- **`insecureRegistries`.** Reach named `host:port` registries over plain HTTP for
  a controlled in-cluster registry, scoped per host so every other registry stays
  HTTPS-only. The controller, the managed dashboard and the managed Evidence
  Server all inherit it.

Also adds a `pacto-dev-gateway` chart that installs Envoy Gateway and a
`GatewayClass` for local development, so the Gateway API path can be exercised on
a laptop cluster.

Backwards compatible: every new capability is off by default and no existing
value, CRD field or status shape changes.
