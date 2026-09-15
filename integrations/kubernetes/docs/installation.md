# Install the Kubernetes operator

The operator is distributed as a Helm chart and a controller image. Coordinates
and versions are on the [published artifacts](artifact-hub.md) page; every value flag is
on the [Helm reference](helm-reference.md).

## Prerequisites

- A Kubernetes cluster (the operator watches cluster-wide by default). The
  acceptance suite runs against the Kubernetes version shipped by the default
  [kind](https://kind.sigs.k8s.io/) node image -- v1.35 at the time of writing.
  The chart declares no `kubeVersion` floor, so older clusters are untested
  rather than blocked.
- Helm 3.8 or newer (OCI registry support).
- The [`pacto` CLI](../../installation.md) for the steps after the install --
  publishing the contract the operator will bind to, minting the evidence trust
  store, and querying the fleet from outside the cluster. The Helm install
  itself does not need it.
- Cluster-admin permissions to install the CRDs and the operator's `ClusterRole`
  (see [RBAC](rbac.md)).
- Network access from the cluster to the registry holding your contracts. For a
  private repository, put credentials in a Secret and name it in
  `spec.contractRef.pullSecretRef` on each `Pacto` resource. Without one the
  operator pulls anonymously and a private contract reports `Unknown` with an
  authentication message.

## Install with Helm

The chart is published as an OCI artifact. Installing it also installs the CRDs
(bundled under the chart's `crds/` directory) and, by default, the operator-managed
dashboard.

!!! warning "At chart defaults the operator can escalate its own privileges"

    Managing the dashboard means creating the dashboard's own RBAC, so the
    default install grants the operator unrestricted `create` on `clusterroles`
    and `clusterrolebindings` — enough to grant itself anything in the cluster.
    Its *observation* of your workloads is read-only; the install as a whole is
    not. Install with `--set dashboard.enabled=false` and deploy the dashboard
    yourself if that does not fit your threat model. [RBAC](rbac.md) lists every
    rule, generated from the chart.

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace
```

Pin a chart version with `--version` for reproducible installs (see the
[compatibility table](upgrade.md#version-compatibility)). The version below is
the currently published chart:

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

!!! note "Three observation features are not reachable from the chart"

    Metrics observation, active health probing and name-match discovery are
    controller command-line flags, and the chart renders a fixed argument list
    with no `extraArgs`. There is no `--set` for them on this install path, and a
    flag patched onto the Deployment by hand disappears the next time you run
    `helm upgrade`. Read
    [Opt-in features](limitations.md#opt-in-features) **before** you plan around
    any of the three — particularly active health probing, because without it a declared health endpoint can be confirmed but never
    contradicted.

!!! warning "The dashboard has no authentication — do not expose it"

    The dashboard ships no login, no API key and no authorization: anyone who
    can reach it reads every contract, dependency and compliance result, and can
    make it pull from your registries through `POST /api/resolve`. Its only
    browser protection is a same-origin check on mutating requests, which a
    non-browser client (`curl`) does not trip. The chart therefore defaults
    `dashboard.service.type` to `ClusterIP` with `dashboard.ingress.enabled` and
    `dashboard.httpRoute.enabled` off. Reach it with `kubectl port-forward`, or
    put your own authenticating proxy in front of it before turning any of those
    on.

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
`configMap` for small static exports; exactly one of the two per source. Whoever
owns that storage owns producing and rotating the exports: Pacto ships **no OTLP
receiver** and deploys no collector, so nothing listens on 4317 or 4318.

[Observation sources](../../observation-sources.md) is the reference for the
rest — why `name` is an identity rather than a label, what a name collision does,
the read root each source is confined to, and why an unreadable source and a
stale one are different answers.

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

Two Deployments, because the default install manages the dashboard for you:
`pacto-operator` is the controller Helm created, `pacto-dashboard` is the one
the controller created in turn. **If you installed with
`--set dashboard.enabled=false`, you get `pacto-operator` alone** — the healthy
log below has no dashboard reconciler lines, and the port-forward and
[Bind your first contract](#bind-your-first-contract) steps, which use the
dashboard's own published contract as the example, need a contract of your own.
Both CRDs are registered either way:

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

A healthy start ends with the controller's workers and the dashboard
reconciler:

```text
INFO  dashboard  Starting dashboard reconciler  {"enabled": true, "image": "ghcr.io/trianalab/pacto/dashboard:3.3.1", ...}
INFO  Starting Controller  {"controller": "pacto", "controllerKind": "Pacto"}
INFO  Starting workers     {"controller": "pacto", "worker count": 1}
INFO  dashboard  Dashboard resources reconciled successfully
```

Open the dashboard by forwarding its Service (there is no Ingress by default):

```bash
kubectl port-forward -n pacto-operator-system svc/pacto-dashboard 3000:3000
```

### Scraping the metrics

`metrics.enabled` is `true` by default, so the install already publishes a
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
write alerting rules that tolerate a missing series rather than reading `0` as a
failing gate.

Two things the chart does not do for you. `metrics.serviceMonitor.enabled` is
`false`, so nothing is scraped until you turn it on (or point your own scrape
config at the Service). And because `metrics.secure` is `true`, the endpoint sits
behind the controller-runtime authn/authz filter: **an unauthorised scrape gets
`403`, not an empty page.** The chart packages no reader role, so grant one to
the ServiceAccount that scrapes:

```bash
kubectl create clusterrole pacto-metrics-reader \
  --non-resource-url=/metrics --verb=get
kubectl create clusterrolebinding pacto-metrics-reader \
  --clusterrole=pacto-metrics-reader \
  --serviceaccount=monitoring:prometheus-k8s
```

Setting `metrics.secure=false` also works and is the wrong trade for a shared
cluster: the gauges name every contract and namespace you have bound.

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
(`3.2.1`), snapshotted every tag it saw as an immutable `PactoRevision`
(`kubectl get pactorevisions -n pacto-operator-system`), observed the Deployment
and Service behind `pacto-dashboard` and wrote `status.contractStatus`.

**`Unknown` is the expected first result, and it is not a failure.** Zero errors
and zero warnings means nothing contradicted the contract; the operator could
not observe four of the things the contract declares. `kubectl describe
pacto pacto-dashboard -n pacto-operator-system` names each one:

```text
message: interface "http-api" cannot be observed in this environment
code:    OBSERVATION_UNSUPPORTED
```

An interface has no port until you say which Service port serves it — that is
Kubernetes knowledge the platform-agnostic contract deliberately does not carry.
Add the binding and the interface and its health capability resolve:

```yaml
  target:
    serviceName: pacto-dashboard
    interfaceBindings:
      - interface: http-api
        servicePort: 3000
```

Two findings remain on this path: the `metrics` capability needs
`--enable-metrics-observation`, which [the chart does not
expose](limitations.md#opt-in-features), and the `default` configuration needs a
`configBindings` entry naming the ConfigMap or Secret that backs it. See
[Contract bindings](contract-bindings.md) for both, and [Runtime
observations](runtime-observations.md) for how each finding maps to a status.

## Uninstall

```bash
helm uninstall pacto-operator --namespace pacto-operator-system
```

That removes the controller and every component it manages: the dashboard's and
the Evidence Server's Deployments, Services, ServiceAccount and generated
credentials Secret are owner-referenced to the controller Deployment, so
Kubernetes garbage-collects them. Five things survive:

**The CRDs and your `Pacto` resources.** Helm never deletes CRDs. Removing them
deletes every `Pacto` and `PactoRevision` with them. The operator sets no
finalizers, so this returns immediately even with resources still bound:

```bash
kubectl delete crd pactos.pacto.trianalab.io pactorevisions.pacto.trianalab.io
```

**The dashboard's cluster-scoped RBAC.** A cluster-scoped object cannot be owned
by a namespaced one, so the `pacto-dashboard` `ClusterRole` and
`ClusterRoleBinding` the operator created outlive the release. Nothing removes
them:

```bash
kubectl delete clusterrole pacto-dashboard
kubectl delete clusterrolebinding pacto-dashboard
```

**Anything you created by hand.** Helm only owns what Helm rendered, so the
objects the optional features asked you to create stay behind:

```bash
# Only if you enabled the Evidence Server
kubectl delete secret pacto-evidence-trust -n pacto-operator-system

# Only if you set evidence.registry.credentialsSecret. The chart never creates
# that Secret -- it points at one you already had -- so delete it by its own
# name, not by the value name below.
kubectl delete secret YOUR_REGISTRY_CREDENTIALS_SECRET -n pacto-operator-system

# Only if you granted metrics observation (see Limitations)
kubectl delete clusterrole metrics-observation-role
kubectl delete clusterrolebinding metrics-observation-rolebinding
```

**The leader-election Lease.** Helm did not render it — controller-runtime
created it at startup, with no owner to garbage-collect it. It is inert once the
controller is gone and a reinstall reuses it, so it only matters if you leave the
namespace in place:

```bash
kubectl delete lease a4917283.pacto.io -n pacto-operator-system
```

**The namespace**, if `--create-namespace` created it: `kubectl delete namespace
pacto-operator-system`. Deleting it takes the Lease with it.

Order does not matter. To see what is left, ask before deleting the namespace:

```bash
kubectl get all,sa,secret,lease,role,rolebinding -n pacto-operator-system
kubectl get clusterrole,clusterrolebinding | grep pacto
```

Everything that answers is on the list above — plus Kubernetes' own
`default` ServiceAccount and `kube-root-ca.crt` ConfigMap, which belong to the
namespace rather than to Pacto.
