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
  oci://ghcr.io/trianalab/pacto-operator/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace
```

Pin a specific chart version with `--version` (recommended for reproducible
installs; see the [compatibility table](upgrade.md#version-compatibility)):

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto-operator/charts/pacto-operator \
  --version 4.7.0 \
  --namespace pacto-operator-system --create-namespace
```

### Common overrides

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto-operator/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace \
  --set controller.watchNamespace=my-namespace \
  --set metrics.serviceMonitor.enabled=true \
  --set dashboard.enabled=false
```

- `controller.watchNamespace` restricts observation to a single namespace (empty
  means cluster-wide).
- `metrics.serviceMonitor.enabled` creates a Prometheus `ServiceMonitor`.
- `dashboard.enabled` toggles the operator-managed dashboard.

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
