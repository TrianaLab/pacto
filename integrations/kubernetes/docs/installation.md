# Install the Kubernetes operator

Install the operator with one Helm command, then point it at a contract and it
tells you whether the running service matches. This page takes you from an
empty cluster to your first verdict.

The operator ships as a Helm chart and a controller image. Coordinates are on
the [published artifacts](artifact-hub.md) page; every value is on the
[Helm reference](helm-reference.md).

## Prerequisites

- A Kubernetes cluster (the operator watches cluster-wide by default). The
  chart declares no `kubeVersion` floor. The acceptance suite runs on whatever
  [kind](https://kind.sigs.k8s.io/) ships by default, so older clusters are
  untested. The chart still installs on them.
- Helm 3.8 or newer (OCI registry support).
- Cluster-admin permissions to install the CRDs, the operator's `ClusterRole`
  and its namespaced `Role` (see [RBAC](rbac.md)). A CRD (custom resource
  definition) teaches Kubernetes a new kind of object, here `Pacto`.
- No Pod Security exemption for the release namespace: the operator, the
  dashboard and the Evidence Server run under `restricted`. The optional
  [node agent](runtime-conformance.md#turn-it-on) gets its own namespace,
  which the chart creates privileged.
- The [`pacto` CLI](../../installation.md) for the steps after the install. The
  Helm install itself does not need it.
- Network access from the cluster to the registry holding your contracts. For a
  private repository, put credentials in a Secret and name it in
  `spec.contractRef.pullSecretRef` on each `Pacto` resource; without one the
  operator pulls anonymously and a private contract reports `Unknown`.

## Install with Helm

The chart lives in an OCI registry. Installing it also installs the CRDs and,
by default, a dashboard the operator runs for you. The CRDs are part of the
release: `helm upgrade` updates them and `helm uninstall` keeps them.

!!! warning "At chart defaults the operator can escalate its own privileges"

    Managing the dashboard means creating the dashboard's own RBAC, so the
    default install grants the operator unrestricted `create` on `clusterroles`
    and `clusterrolebindings`, enough to grant itself anything in the cluster.
    Its *observation* of your workloads is read-only, and the install as a whole
    can write. Use `--set dashboard.enabled=false` if that does not fit your
    threat model, and deploy the dashboard yourself from the
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

To manage the CRDs yourself, for example from a separate GitOps source, set
`crds.install=false` and apply them before the install. The operator does not
start without them:

--8<-- "integrations/kubernetes/docs/generated/_crd-apply.md"

With `delivery.enabled=true`, also apply the
[delivery CRDs](delivery-setup.md#install). `crds.install` covers them too.

### Quickstart with a bundled registry and sign-in

For an evaluation cluster, the chart ships the profile `values-quickstart.yaml`.
It installs [zot](https://zotregistry.dev) as an in-cluster registry with
persistence and native OCI referrers, installs [Dex](https://dexidp.io) as the
dashboard's identity provider and turns on [delivery](delivery-setup.md) with
its recorder writing to zot. The chart generates the dashboard's client secret,
the first sign-in password and the recorder's signing key at install and keeps
them across upgrades.

```bash
helm pull oci://ghcr.io/trianalab/pacto/charts/pacto-operator --untar
helm install pacto-operator ./pacto-operator \
  --namespace pacto-operator-system --create-namespace \
  -f pacto-operator/values-quickstart.yaml
```

The profile is for evaluation. zot and Dex speak plain HTTP, zot accepts
anonymous pushes, Dex keeps sessions in memory and the operator can read the
recorder's key. The registry host in the profile names the namespace
`pacto-operator-system`; to install elsewhere, edit it in the file first.

The browser and the dashboard reach Dex at the same issuer URL,
`http://pacto-dex:5556/dex`. To sign in, point that name at your machine,
forward both Services and read the password of `admin@example.com`:

```bash
echo "127.0.0.1 pacto-dex" | sudo tee -a /etc/hosts
kubectl -n pacto-operator-system port-forward svc/pacto-dex 5556:5556 &
kubectl -n pacto-operator-system port-forward svc/pacto-dashboard 3000:3000 &
kubectl -n pacto-operator-system get secret pacto-dashboard-oidc \
  -o jsonpath='{.data.admin-password}' | base64 -d
```

The dashboard sends every request with the signed-in person's token, so
per-person Kubernetes RBAC needs the API server to trust the Dex issuer through
[structured authentication](dashboard-authentication.md#match-the-api-servers-claim-mapping)
with the profile's claim mapping (`email` and `groups`, no prefixes). That is
an API server setting, which no chart can set; kind takes it at cluster
creation. The API server also accepts only an `https` issuer, so the bundled
Dex needs TLS first. Until the API server trusts it, a Dex sign-in completes
and every request is refused, so use
[token sign-in](dashboard-authentication.md#token-sign-in), which the profile
turns on.

### Common overrides

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace \
  --set controller.watchNamespace=my-namespace \
  --set metrics.serviceMonitor.enabled=true \
  --set dashboard.enabled=false
```

- `controller.watchNamespace` watches one namespace only (empty means the
  whole cluster).
- `metrics.serviceMonitor.enabled` creates a Prometheus `ServiceMonitor`.
- `dashboard.enabled` turns the operator-managed dashboard on or off.

The CRDs are cluster-wide and belong to one release. For a second release, for
example one per watched namespace, set `crds.install=false`, or its install
fails with `invalid ownership metadata`. The same happens on a reinstall under
a new release name after an uninstall, because the kept CRDs still name the old
release. Point them at the new one first:

```bash
for crd in $(kubectl get crd -o name | grep pacto.trianalab.io); do
  kubectl annotate "$crd" --overwrite \
    meta.helm.sh/release-name=NEW_RELEASE \
    meta.helm.sh/release-namespace=NEW_NAMESPACE
done
```

Three checks are off by default because they cost something:

- `controller.enableProbing` lets the operator send requests to your Services
  to check health and protocol.
- `controller.enableMetricsObservation` checks the metrics capability.
- `nodeAgent.enabled` runs a small agent on every node that watches which ports
  your pods really listen on and which services they really connect to. It has
  requirements of its own, listed in
  [Runtime conformance](runtime-conformance.md).

[Opt-in features](limitations.md#opt-in-features) explains each one.
Name-match discovery has no chart value.

!!! warning "Keep the dashboard private unless sign-in protects it"

    By default anyone who can reach the dashboard reads every contract,
    dependency and compliance result. The chart therefore defaults
    `dashboard.service.type` to `ClusterIP` with `dashboard.ingress.enabled`
    and `dashboard.httpRoute.enabled` off. Reach it with `kubectl
    port-forward`. Before you turn any of those on, put an authenticating proxy
    in front of it or configure
    [sign-in](dashboard-authentication.md). See
    [Security](../../dashboard-docker.md#security).

### Resources and probes

The controller, the delivery promoter and the delivery recorder set CPU
requests and memory limits, and no CPU limit. A CPU limit throttles a process
on a busy node until its liveness probe times out and the kubelet restarts
it. The request still reserves each one's share. The liveness probe allows
5 seconds per answer and restarts a process after 2 minutes of failures. Set
`resources`, `delivery.promoter.resources` or `delivery.recorder.resources` to
change the defaults, listed in the [Helm reference](generated/helm-reference.md).

The promoter wakes a Stage when an object it reads changes and rechecks it
once per `delivery.promoter.resync` (1m by default). An operation waiting on
Flux is checked once per `delivery.promoter.poll` (3s by default), so the
promoter's CPU grows with the number of waiting operations. On a large fleet
raise the poll or the promoter's CPU request.

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
read as exactly `<mount>/<file>`, with no directory scan and no writes. Give each
source either `existingClaim` (real exports, written by some other workload) or
`configMap` (small static exports), never both. `name` is the Data Source
identity and must be unique across *every* source the dashboard assembles, so
`local`, `catalog`, `target-state`, `evidence-http`, `oci` and `cache` are
already taken, as is the Kubernetes source's identity: your current kube
context name, or `k8s` when no context is set. A collision is refused, naming
both claimants. Whoever owns the storage owns producing and rotating the
exports: Pacto ships **no OTLP receiver**, so nothing listens on 4317 or 4318.

[Sources](../../operational-graph-reference.md#sources) lists every source the graph reads,
and [Knowledge](../../operational-graph.md#knowledge) tells an unreadable source
from a stale one.

### The Evidence Server is off by default

`evidence.enabled` is `false`. See [The Evidence Server](evidence-server.md).

### Keep the delivery namespace to cluster admins

With `delivery.enabled`, the promoter and the recorder run in
`delivery.namespace` (`pacto-delivery`). Anyone who can create a pod there can
run it as the promoter's service account, which moves any labelled pointer and
writes every Stage's and Operation's status, or as the recorder's, which signs
records. Anyone who can edit a ConfigMap there can change the identity policy
and the upstream trust config. Grant write access in that namespace to cluster
admins only, and give nobody else a binding there.

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
`pacto-dashboard` is the one the controller created in turn. With
`nodeAgent.enabled=true` you also get a `pacto-node-agent` DaemonSet (one pod
per node). **With `--set
dashboard.enabled=false` you get `pacto-operator` alone**: the log below has no
dashboard reconciler lines, you skip the port-forward and [Bind your first
contract](#bind-your-first-contract) needs a contract of your own. The three
CRDs are registered either way:

```bash
kubectl get crds | grep pacto.trianalab.io
```

```text
noderuntimeobservations.pacto.trianalab.io   2026-08-22T21:24:59Z
pactorevisions.pacto.trianalab.io   2026-08-22T21:24:59Z
pactos.pacto.trianalab.io           2026-08-22T21:24:59Z
```

If a Deployment never becomes available, read the controller's log:

```bash
kubectl -n pacto-operator-system logs deploy/pacto-operator
```

A healthy start ends with the workers and the dashboard reconciler:

```text
INFO  dashboard  Starting dashboard reconciler  {"enabled": true, "image": "ghcr.io/trianalab/pacto/dashboard:4.0.0", ...}
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
| `pacto_contract_status` | Info-style: `1` for the resource's current status, `0` for every other. The `status` label takes all seven CRD values, so `NotEvaluated` is always `0`: the operator [never writes it](limitations.md#notevaluated-is-reserved). |
| `pacto_readiness_score` | The derived readiness score, 0-100. |
| `pacto_readiness_gate` | `1` when the gate passes, `0` when it does not. |
| `pacto_readiness_status` | Info-style over the gate state: `Satisfied`, `BelowMinScore` or `Expired`. |
| `pacto_readiness_checks` | How many claims sit at each declared `status`: `done`, `partial`, `not-done`, `deferred`. |

The four readiness gauges are emitted only for contracts that declare a
`readiness:` block. For a contract without one the series is **absent**, so
write alerting rules that tolerate a missing series.

Two things the chart does not do for you. `metrics.serviceMonitor.enabled` is
`false`, so nothing is scraped until you turn it on. And `metrics.secure` is
`true`, so the endpoint sits behind the controller-runtime authn/authz filter:
**an unauthorised scrape gets `403`.** The chart packages no
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

**`Unknown` is the expected first result.** Zero errors
and zero warnings means nothing contradicted the contract; the operator could
not observe four of the things it declares. `kubectl describe pacto
pacto-dashboard -n pacto-operator-system` names each one:

```text
message: interface "http-api" cannot be observed in this environment
code:    OBSERVATION_UNSUPPORTED
```

An interface has no port until you say which Service port serves it. That is
Kubernetes knowledge, and the platform-agnostic contract leaves it to the binding. Add the
binding and the interface and its health capability resolve:

```yaml
  target:
    serviceName: pacto-dashboard
    interfaceBindings:
      - interface: http-api
        servicePort: 3000
```

Two findings remain: the `metrics` capability needs
`controller.enableMetricsObservation` (see
[Opt-in features](limitations.md#opt-in-features)), and the `default` configuration needs a
`configBindings` entry naming the ConfigMap or Secret behind it. See
[Contract bindings](contract-bindings.md) for both, and [Runtime
observations](runtime-observations.md) for how a finding maps to a status.

## Uninstall

```bash
helm uninstall pacto-operator --namespace pacto-operator-system
```

That removes the controller and everything owner-referenced to it, dashboard and
Evidence Server included. Five things survive, in any order:

- **The CRDs and your `Pacto` resources.** The chart marks every CRD
  `helm.sh/resource-policy: keep`, because deleting one takes every `Pacto`,
  `PactoRevision` and `NodeRuntimeObservation` with it. No finalizers, so the
  delete returns immediately.
- **The dashboard's cluster-scoped RBAC**, which a namespaced owner cannot own.
- **Anything you created by hand**: the Evidence Server's trust Secret, your
  registry-credentials Secret, a `metrics-observation-role` ClusterRole you
  applied yourself.
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
kubectl delete crd pactos.pacto.trianalab.io pactorevisions.pacto.trianalab.io \
  noderuntimeobservations.pacto.trianalab.io
kubectl delete clusterrole,clusterrolebinding pacto-dashboard
kubectl delete clusterrole metrics-observation-role               # if granted
kubectl delete clusterrolebinding metrics-observation-rolebinding # if granted
kubectl delete namespace pacto-operator-system   # takes the Lease and Secrets
```
