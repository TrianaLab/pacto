# Install the Kubernetes operator

The operator is distributed as a Helm chart and a controller image. Coordinates
and versions are on the [Artifact Hub](artifact-hub.md) page; every value flag is
on the [Helm reference](helm-reference.md) page.

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

Pin a specific chart version with `--version` (recommended for reproducible
installs; see the [compatibility table](upgrade.md#version-compatibility)). The
version below is the currently published chart:

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
    any of the three — particularly if you are evaluating active health probing,
    because without it a declared health endpoint can be confirmed but never
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

### The Evidence Server is off by default

The dashboard is the only managed component a default install deploys.
`evidence.enabled` is `false`, and turning it on has three requirements the
chart will not guess for you. Two of them are things you create first.

**Create the trust store.** `pacto evidence keygen` mints an Ed25519 pair and
names the public key after the trust binding the server reads —
`<producerId>__<keyId>.pub`, or a bare `<keyId>.pub` when there is a single
producer:

```bash
pacto evidence keygen --out ./keys --producer acme-ci --key-id release-2026
```

```text
key id:      release-2026
private key: keys/release-2026.key
public key:  keys/acme-ci__release-2026.pub
```

The Secret is mounted whole and read-only at `/etc/pacto/trust`, so **each
Secret key has to be the public-key filename** — which is exactly what
`--from-file` gives you. One `--from-file` per trusted producer:

```bash
kubectl create secret generic pacto-evidence-trust \
  --namespace pacto-operator-system \
  --from-file=keys/acme-ci__release-2026.pub
```

The private `.key` stays with the producer that signs; the cluster never needs
it. [Evidence security](../../evidence-security.md) covers rotation and
multi-producer trust.

**Get the subject digest.** A subject is one immutable contract revision, and
`pacto push` prints the digest of the revision it just published:

```text
Pushed payments-api@2.1.0 -> registry.example.com/your-org/your-service-pacto:2.1.0
Digest: sha256:<64 hex characters>
```

Then install:

```bash
helm install pacto-operator \
  oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace \
  --set evidence.enabled=true \
  --set 'evidence.registry.subjects[0]=oci://registry.example.com/your-org/your-service-pacto@sha256:<digest>' \
  --set evidence.trust.existingSecret=pacto-evidence-trust
```

- **At least one subject.** `evidence.registry.subjects` lists the exact,
  immutable contract revisions evidence may be reported against, each an
  `oci://<repo>@sha256:<digest>` reference. The chart's schema rejects an empty
  list, so `helm install` fails before anything reaches the cluster:
  `at '/evidence/registry/subjects': minItems: got 0, want 1`. It rejects a
  short or tag-shaped reference the same way — the digest has to be all 64 hex
  characters.
- **A trust store.** `evidence.trust.existingSecret` names the Secret you
  created above. The chart does **not** enforce this one, so an install without
  it succeeds and the operator then exits at startup with `evidence enabled but
  no trust secret set: signature verification is mandatory`. Verification is
  never optional.
- **A registry that serves the native Referrers API.** Evidence is stored as an
  OCI 1.1 referrer of the subject digest, and Pacto does not fall back to the
  tag-based scheme. **GHCR does not qualify**, and neither does CNCF
  distribution (`registry:2`, `registry:3`) — a contract you want to carry
  evidence has to live in a conformant registry, because evidence attaches in
  the contract's own repository. See [Evidence in
  OCI](../../evidence-oci-storage.md) for the registries this was checked
  against.

If that registry is private, there is a fourth thing you create yourself: a
`kubernetes.io/dockerconfigjson` Secret named by
`evidence.registry.credentialsSecret`. It is mounted read-only as a
`DOCKER_CONFIG` directory, so the server authenticates exactly the way
`pacto pull` does — there is no second credential model. Leave the value empty
for an anonymous or in-cluster registry. Whatever name you pick is the one
[Uninstall](#uninstall) asks you to delete.

See the [Helm reference](helm-reference.md) for the full value list and the
[Operator configuration](operator-configuration.md) page for the underlying
controller flags each value maps to.

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
dashboard's own published contract as the example, need a contract of your own
instead. Both CRDs should be registered either way:

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
INFO  dashboard  Starting dashboard reconciler  {"enabled": true, "image": "ghcr.io/trianalab/pacto/dashboard:3.2.1", ...}
INFO  Starting Controller  {"controller": "pacto", "controllerKind": "Pacto"}
INFO  Starting workers     {"controller": "pacto", "worker count": 1}
INFO  dashboard  Dashboard resources reconciled successfully
```

Open the dashboard by forwarding its Service (there is no Ingress by default):

```bash
kubectl port-forward -n pacto-operator-system svc/pacto-dashboard 3000:3000
```

### If you enabled the Evidence Server

`evidence.enabled=true` adds a third Deployment, `pacto-evidence`, created by
the controller the same way the dashboard is:

```text
NAME              READY   UP-TO-DATE   AVAILABLE   AGE
pacto-dashboard   1/1     1            1           16s
pacto-evidence    1/1     1            1           14s
pacto-operator    1/1     1            1           21s
```

`1/1` here means more than "the process started". Readiness is
`GET /api/evidence/v1/ready`, which answers `503` until **every** subject in
`evidence.registry.subjects` resolves in the registry *and* answers native
Referrers discovery. So a `pacto-evidence` stuck at `0/1` is nearly always a
subject the cluster cannot pull or a registry without the Referrers API — read
its own log, not the controller's:

```bash
kubectl -n pacto-operator-system logs deploy/pacto-evidence
```

The Service is `pacto-evidence` on port `8686`. Producers inside the cluster
POST signed envelopes to its ingestion endpoint:

```text
http://pacto-evidence.pacto-operator-system.svc:8686/api/evidence/v1/envelopes
```

A `pacto fleet` outside the cluster consumes the same server's read-only
contribution by **base** URL — `--evidence-url` appends
`/api/evidence/v1/targets` itself, so do not include it:

```bash
kubectl port-forward -n pacto-operator-system svc/pacto-evidence 8686:8686 &
pacto fleet search --evidence-url http://127.0.0.1:8686
```

Nothing durable lives in the cluster: there is no PersistentVolumeClaim and no
data volume, because the registry is the store. Delete and recreate the
Deployment and the accepted evidence is still there.
[Evidence in OCI](../../evidence-oci-storage.md) covers what is written and
where; [the evidence protocol](../../evidence-protocol.md#ingestion-api) lists all five
endpoints.

## Bind your first contract

A `Pacto` resource points at a contract and at the Service to observe. The
dashboard the operator just deployed publishes its own contract, so you can bind
a real one without pushing anything first. Save this as `pacto-dashboard.yaml`:

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
and zero warnings means nothing contradicted the contract; the operator simply
could not observe four of the things the contract declares. `kubectl describe
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

That removes the controller and, with it, every component it manages: the
dashboard's and the Evidence Server's Deployments, Services, ServiceAccount and
generated credentials Secret are all owner-referenced to the controller
Deployment, so Kubernetes garbage-collects them. Five things survive, by design
or by scope:

**The CRDs and your `Pacto` resources.** Helm never deletes CRDs. Removing them
deletes every `Pacto` and `PactoRevision` with them. The operator sets no
finalizers, so this returns immediately even with resources still bound:

```bash
kubectl delete crd pactos.pacto.trianalab.io pactorevisions.pacto.trianalab.io
```

**The dashboard's cluster-scoped RBAC.** A cluster-scoped object cannot be owned
by a namespaced one, so the `pacto-dashboard` `ClusterRole` and
`ClusterRoleBinding` the operator created outlive the release. Nothing uses them
once the operator is gone, but nothing removes them either:

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
controller is gone, and a reinstall reuses it, so it only matters if you are
leaving the namespace in place:

```bash
kubectl delete lease a4917283.pacto.io -n pacto-operator-system
```

**The namespace**, if `--create-namespace` created it: `kubectl delete namespace
pacto-operator-system`. Deleting it takes the Lease with it.

Order does not matter — none of these block on each other. To see for yourself
what is left, ask before deleting the namespace:

```bash
kubectl get all,sa,secret,lease,role,rolebinding -n pacto-operator-system
kubectl get clusterrole,clusterrolebinding | grep pacto
```

Everything that answers is on the list above — plus Kubernetes' own
`default` ServiceAccount and `kube-root-ca.crt` ConfigMap, which belong to the
namespace rather than to Pacto.
