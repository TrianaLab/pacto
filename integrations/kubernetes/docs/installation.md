# Install the Kubernetes operator

The operator ships as a Helm chart and a controller image. Coordinates are on the
[published artifacts](artifact-hub.md) page; every value is on the
[Helm reference](helm-reference.md).

## Prerequisites

- A Kubernetes cluster (the operator watches cluster-wide by default). The
  chart declares no `kubeVersion` floor. The acceptance suite runs on whatever
  [kind](https://kind.sigs.k8s.io/) ships by default, so older clusters are
  untested rather than blocked.
- Helm 3.8 or newer (OCI registry support).
- Cluster-admin permissions to install the CRDs and the operator's `ClusterRole`
  (see [RBAC](rbac.md)).
- The [`pacto` CLI](../../installation.md) for the steps after the install. The
  Helm install itself does not need it.
- Network access from the cluster to the registry holding your contracts. For a
  private repository, put credentials in a Secret and name it in
  `spec.contractRef.pullSecretRef` on each `Pacto` resource; without one the
  operator pulls anonymously and a private contract reports `Unknown`.

## Install with Helm

The chart is published as an OCI artifact. Installing it also installs the CRDs
(bundled under the chart's `crds/` directory) and, by default, the
operator-managed dashboard.

!!! warning "At chart defaults the operator can escalate its own privileges"

    Managing the dashboard means creating the dashboard's own RBAC, so the
    default install grants the operator unrestricted `create` on `clusterroles`
    and `clusterrolebindings` — enough to grant itself anything in the cluster.
    Its *observation* of your workloads is read-only; the install as a whole is
    not. Use `--set dashboard.enabled=false` if that does not fit your threat
    model, and deploy the dashboard yourself from the
    [container manifest](../../dashboard-docker.md#kubernetes-deployment).
    [RBAC](rbac.md) lists every rule, generated from the chart.

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace
```

Pin a chart version with `--version` for a reproducible install (see the
[compatibility table](upgrade.md#version-compatibility)). The currently
published chart:

--8<-- "integrations/kubernetes/docs/generated/_install-command.md"

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

Metrics observation, active health probing and name-match discovery are
controller flags the chart does not expose:
[Opt-in features](limitations.md#opt-in-features) explains what that costs you.

!!! warning "The dashboard has no authentication — do not expose it"

    Anyone who can reach it reads every contract, dependency and compliance
    result. The chart therefore defaults `dashboard.service.type` to `ClusterIP`
    with `dashboard.ingress.enabled` and `dashboard.httpRoute.enabled` off;
    reach it with `kubectl port-forward`, or put your own authenticating proxy
    in front of it before turning any of those on. See
    [Security](../../dashboard-docker.md#security).

### Offline trace sources for the dashboard

Observed dependencies reach the dashboard as **offline OTLP/JSON trace exports**.
The operator mounts them for you:

```yaml
dashboard:
  enabled: true
  observation:
    sources:
      - name: orders                      # stable Data Source identity
        file: traces.json                 # relative to this source's mount
        existingClaim: orders-trace-export
```

Each source is mounted **read-only** at `/var/lib/pacto/observation/<name>/` and
read as exactly `<mount>/<file>` — no directory scanning, no writes. Give each
source either `existingClaim` (real exports, written by some other workload) or
`configMap` (small static exports), never both. `name` is the Data Source
identity and must be unique across *every* source the dashboard assembles, so
`local`, `catalog`, `target-state`, `evidence-http`, `oci` and `cache` are
already taken, as is the Kubernetes source's identity — your current kube
context name, or `k8s` when no context is set. A collision is refused, naming
both claimants. Whoever owns the storage owns producing and rotating the
exports: Pacto ships **no OTLP receiver**, so nothing listens on 4317 or 4318.

[Sources](../../operational-graph.md#sources) lists every source the graph reads,
and [Knowledge](../../operational-graph.md#knowledge) tells an unreadable source
from a stale one.

### The Evidence Server is off by default

`evidence.enabled` is `false`. See [The Evidence Server](evidence-server.md).

## Verify the install

```bash
kubectl -n pacto-operator-system get deploy
```

```text
NAME              READY   UP-TO-DATE   AVAILABLE   AGE
pacto-dashboard   1/1     1            1           16s
pacto-operator    1/1     1            1           21s
```

Two Deployments: `pacto-operator` is the controller Helm created,
`pacto-dashboard` is the one the controller created in turn. **With `--set
dashboard.enabled=false` you get `pacto-operator` alone** — no dashboard
reconciler lines in the log below, no port-forward and [Bind your first
contract](#bind-your-first-contract) needs a contract of your own. Both CRDs are
registered either way:

```bash
kubectl get crds | grep pacto.trianalab.io
```

```text
pactorevisions.pacto.trianalab.io   2026-08-22T21:24:59Z
pactos.pacto.trianalab.io           2026-08-22T21:24:59Z
```

If a Deployment never becomes available, read the controller's log:

```bash
kubectl -n pacto-operator-system logs deploy/pacto-operator
```

A healthy start ends with the workers and the dashboard reconciler:

```text
INFO  dashboard  Starting dashboard reconciler  {"enabled": true, "image": "ghcr.io/trianalab/pacto/dashboard:3.3.3", ...}
INFO  Starting Controller  {"controller": "pacto", "controllerKind": "Pacto"}
INFO  Starting workers     {"controller": "pacto", "worker count": 1}
INFO  dashboard  Dashboard resources reconciled successfully
```

Open the dashboard by forwarding its Service; there is no Ingress by default:

```bash
kubectl port-forward -n pacto-operator-system svc/pacto-dashboard 3000:3000
```

### Scraping the metrics

`metrics.enabled` is `true`, so the install already publishes a
`pacto-operator-metrics` Service on port 8443. Five gauges are exported, all
labelled `name` and `namespace` after the `Pacto` resource:

| Metric | What it is |
|---|---|
| `pacto_contract_status` | Info-style: `1` for the resource's current status, `0` for every other. The `status` label takes all seven CRD values, so `NotEvaluated` is always `0` — the operator [never writes it](limitations.md#notevaluated-is-reserved). |
| `pacto_readiness_score` | The derived readiness score, 0-100. |
| `pacto_readiness_gate` | `1` when the gate passes, `0` when it does not. |
| `pacto_readiness_status` | Info-style over the gate state: `Satisfied`, `BelowMinScore` or `Expired`. |
| `pacto_readiness_checks` | How many claims sit at each declared `status`: `done`, `partial`, `not-done`, `deferred`. |

The four readiness gauges are emitted only for contracts that declare a
`readiness:` block. For a contract without one they are **absent**, not zero —
write alerting rules that tolerate a missing series.

Two things the chart does not do for you. `metrics.serviceMonitor.enabled` is
`false`, so nothing is scraped until you turn it on. And `metrics.secure` is
`true`, so the endpoint sits behind the controller-runtime authn/authz filter:
**an unauthorised scrape gets `403`, not an empty page.** The chart packages no
reader role, so grant one to the ServiceAccount that scrapes:

```bash
kubectl create clusterrole pacto-metrics-reader \
  --non-resource-url=/metrics --verb=get
kubectl create clusterrolebinding pacto-metrics-reader \
  --clusterrole=pacto-metrics-reader \
  --serviceaccount=monitoring:prometheus-k8s
```

`metrics.secure=false` also works and is the wrong trade for a shared cluster:
the gauges name every contract and namespace you have bound.

## Bind your first contract

A `Pacto` resource points at a contract and at the Service to observe. The
dashboard the operator just deployed publishes its own contract, so you can bind
a real one without pushing anything. Save this as `pacto-dashboard.yaml`:

```yaml
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: pacto-dashboard
  namespace: pacto-operator-system
spec:
  contractRef:
    oci: ghcr.io/trianalab/pacto/dashboard-contract
  target:
    serviceName: pacto-dashboard
```

```bash
kubectl apply -f pacto-dashboard.yaml
kubectl get pactos -n pacto-operator-system
```

```text
NAME              STATUS    SERVICE           VERSION   ERRORS   WARNINGS   LAST RECONCILED   AGE
pacto-dashboard   Unknown   pacto-dashboard   3.2.1     0        0          19s               30s
```

The reference carries no tag, so the operator resolved the highest semver tag
(`3.2.1`). It snapshotted every tag it saw as an immutable `PactoRevision`
(`kubectl get pactorevisions -n pacto-operator-system`), observed the Deployment
and Service behind `pacto-dashboard` and wrote `status.contractStatus`.

**`Unknown` is the expected first result, and it is not a failure.** Zero errors
and zero warnings means nothing contradicted the contract; the operator could
not observe four of the things it declares. `kubectl describe pacto
pacto-dashboard -n pacto-operator-system` names each one:

```text
message: interface "http-api" cannot be observed in this environment
code:    OBSERVATION_UNSUPPORTED
```

An interface has no port until you say which Service port serves it — Kubernetes
knowledge the platform-agnostic contract deliberately does not carry. Add the
binding and the interface and its health capability resolve:

```yaml
  target:
    serviceName: pacto-dashboard
    interfaceBindings:
      - interface: http-api
        servicePort: 3000
```

Two findings remain: the `metrics` capability needs
`--enable-metrics-observation`, which [the chart does not
expose](limitations.md#opt-in-features), and the `default` configuration needs a
`configBindings` entry naming the ConfigMap or Secret behind it. See
[Contract bindings](contract-bindings.md) for both, and [Runtime
observations](runtime-observations.md) for how a finding maps to a status.

## Uninstall

```bash
helm uninstall pacto-operator --namespace pacto-operator-system
```

That removes the controller and everything owner-referenced to it, dashboard and
Evidence Server included. Five things survive, in any order:

- **The CRDs and your `Pacto` resources.** Helm never deletes CRDs, and deleting
  them takes every `Pacto` and `PactoRevision` with them. No finalizers, so it
  returns immediately.
- **The dashboard's cluster-scoped RBAC**, which a namespaced owner cannot own.
- **Anything you created by hand** — the Evidence Server's trust Secret, your
  registry-credentials Secret, the `metrics-observation-role` ClusterRole.
- **The leader-election Lease** `a4917283.pacto.io`, inert once the controller
  is gone and reused on reinstall.
- **The namespace**, if `--create-namespace` created it.

```bash
# Look first: everything that answers is on the list above, plus Kubernetes'
# own default ServiceAccount and kube-root-ca.crt ConfigMap.
kubectl get all,sa,secret,lease,role,rolebinding -n pacto-operator-system
kubectl get clusterrole,clusterrolebinding | grep pacto
```

Then remove what is left:

```bash
kubectl delete crd pactos.pacto.trianalab.io pactorevisions.pacto.trianalab.io
kubectl delete clusterrole,clusterrolebinding pacto-dashboard
kubectl delete clusterrole metrics-observation-role               # if granted
kubectl delete clusterrolebinding metrics-observation-rolebinding # if granted
kubectl delete namespace pacto-operator-system   # takes the Lease and Secrets
```
