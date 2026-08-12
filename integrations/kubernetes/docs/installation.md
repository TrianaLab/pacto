# Installation

The operator is distributed as a Helm chart and a controller image. Coordinates
and versions are on the [Artifact Hub](artifact-hub.md) page; every value flag is
on the [Helm reference](helm-reference.md) page.

## Prerequisites

- A Kubernetes cluster (the operator watches cluster-wide by default).
- Helm 3.8 or newer (OCI registry support).
- Cluster-admin permissions to install the CRDs and the operator's `ClusterRole`
  (see [RBAC](rbac.md)).

## Install with Helm

The chart is published as an OCI artifact. Installing it also installs the CRDs
(bundled under the chart's `crds/` directory) and, by default, the operator-managed
dashboard.

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace
```

Pin a specific chart version with `--version` (recommended for reproducible
installs; see the [compatibility table](upgrade.md#version-compatibility)):

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --version 4.7.0 \
  --namespace pacto-operator-system --create-namespace
```

### Common overrides

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace \
  --set controller.watchNamespace=my-namespace \
  --set metrics.serviceMonitor.enabled=true \
  --set dashboard.enabled=false
```

- `controller.watchNamespace` restricts observation to a single namespace (empty
  means cluster-wide).
- `metrics.serviceMonitor.enabled` creates a Prometheus `ServiceMonitor`.
- `dashboard.enabled` toggles the operator-managed dashboard.

### Offline trace sources for the dashboard

The dashboard's Operational Graph reconciles declared dependencies against
observed ones, and observed evidence arrives as **offline OTLP/JSON trace
exports**. The operator can mount them for you:

```yaml
dashboard:
  enabled: true
  observation:
    sources:
      - name: orders                      # stable Data Source identity
        file: traces.json                 # relative to this source's mount
        existingClaim: orders-trace-export
```

Each source is mounted **read-only** at `/var/lib/pacto/observation/<name>/`, and
the dashboard reads exactly `<mount>/<file>` — no directory scanning, no writes.
Use `existingClaim` for real exports (some other workload writes into the PVC) or
`configMap` for small static exports; exactly one of the two per source. The
`name` is the identity the API and UI show, so reordering the list never renames a
source. Two entries claiming the same name are rejected by the operator when it
reads its configuration; a name that collides with one of the dashboard's *other*
data sources — the live cluster, OCI, the disk cache — is refused by the dashboard
before a snapshot is built, rather than published as one name owned by two sources
(see [Named observation sources](../../operational-graph.md#named-observation-sources)).

Whoever owns that storage owns producing and rotating the exports. Pacto ships
**no OTLP receiver** and deploys no collector: nothing listens on 4317 or 4318. If
a source is missing or malformed the dashboard stays up and reports that Data
Source as unavailable; a readable but old export is a healthy source with stale
evidence, not a claim that a dependency vanished.

See the [Helm reference](helm-reference.md) for the full value list and the
[Operator configuration](operator-configuration.md) page for the underlying
controller flags each value maps to.

## Verify the install

```bash
kubectl -n pacto-operator-system get deploy
kubectl get crds | grep pacto.trianalab.io
```

You should see the operator Deployment running and both CRDs
(`pactos.pacto.trianalab.io`, `pactorevisions.pacto.trianalab.io`) installed.

## Bind your first contract

Create a `Pacto` resource that points at a contract and a Service to observe:

```yaml
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: my-service
  namespace: default
spec:
  contractRef:
    oci: ghcr.io/your-org/my-service-pacto
  target:
    serviceName: my-service
```

```bash
kubectl apply -f my-service-pacto.yaml
kubectl get pactos
kubectl describe pacto my-service
```

The operator resolves the highest semver tag, snapshots it as a `PactoRevision`,
observes the target and sets `status.contractStatus`. See
[Contract bindings](contract-bindings.md) for interface and configuration bindings.

## Uninstall

```bash
helm uninstall pacto-operator --namespace pacto-operator-system
```

Helm does not remove CRDs on uninstall. Remove them explicitly if you want to
delete all `Pacto` resources and their revisions:

```bash
kubectl delete crd pactos.pacto.trianalab.io pactorevisions.pacto.trianalab.io
```
