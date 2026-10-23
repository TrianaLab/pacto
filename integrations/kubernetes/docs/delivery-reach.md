# Delivery reference: Argo CD and several clusters

[Pacto delivery](delivery.md) applies versions through Flux by default. This
page covers the two other set-ups: a Stage applied by Argo CD, and a Stage
that follows a Stage in another cluster.

## Apply through Argo CD

A Stage can point at an Argo CD `Application` instead of a Flux
`OCIRepository`. Turn it on in the delivery chart:

```yaml
argocd:
  enabled: true
```

This gives the promoter `patch` on Argo CD Applications and adds an admission
policy. On a labelled Application only the promoter or break-glass may change
`spec.source.targetRevision`.

The Application must:

- live in the Stage's namespace. Argo CD calls this
  [Applications in any namespace](https://argo-cd.readthedocs.io/en/stable/operator-manual/app-any-namespace/).
  Add the namespace to `application.namespaces` and to the project's
  `sourceNamespaces`.
- have one OCI source (`spec.source.repoURL: oci://…`), not `spec.sources`.
- sync on its own (`syncPolicy.automated`), because the promoter never runs a sync.
- carry the `delivery.pacto.trianalab.io/apply-ref` label.
- start with `targetRevision` empty or at the digest the Stage runs. Any other
  value is a write the promoter did not make, so the first promote freezes the
  Stage.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: payments
  namespace: prod
  labels: {delivery.pacto.trianalab.io/apply-ref: "true"}
spec:
  project: default
  source:
    repoURL: oci://registry/deploy/payments
    path: .
  destination: {server: https://kubernetes.default.svc, namespace: prod}
  syncPolicy: {automated: {}}
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: payments, namespace: prod}
spec:
  service: {name: payments}
  environment: prod
  applyRef: {kind: Application, name: payments}
  candidates:
    upstreamStage: {name: payments, namespace: test}
```

How a promote runs on Argo CD:

1. The promoter sets `spec.source.targetRevision` to the new digest and asks
   Argo CD to refresh.
2. The move counts as applied once the Application is `Synced` and `Healthy`
   with that digest in `status.sync.revision`. That field is the revision
   Argo CD compared against, not proof of a sync: it moves to the new digest
   as soon as the refresh runs, while the live workloads are still the old
   version. Only sync status `Synced` says the live state matches it.
3. A sync of that digest that ends `Failed` or `Error` fails the promote, and
   so does an Application that is `Degraded` once Argo CD has synced that
   digest (sync status `Synced`, or a sync of the digest that `Succeeded`).
   A `Degraded` health before the sync describes the old version and only
   means "not yet". Either failure starts the usual rollback.

Things that differ from Flux:

- **Signatures.** Argo CD does not check signatures. The candidate source
  still does: a Flux `OCIRepository` with `spec.verify`, or an upstream Stage.
  The promoter only ever writes a digest, and a digest names exact bytes, so
  Argo CD fetches exactly the version that was checked.
- **Placement.** Pacto finds where a consumer runs through Flux Kustomizations.
  An Argo CD Stage places nothing that way, so label the Pactos it runs with
  `delivery.pacto.trianalab.io/environment`. An unlabelled Pacto counts in
  every environment.

## Follow a Stage in another cluster

A Stage in one cluster can take its versions from a Stage in another cluster,
such as prod in one cluster following staging in another. The clusters do not
talk to each other. The downstream promoter reads the upstream Stage's signed
records from the registry.

You need:

1. The recorder running in the upstream cluster (see
   [Signed records](delivery-records.md)).
2. The upstream recorder's public key in the downstream promoter's trust
   config, bound to the upstream stream:

   ```yaml
   promoter:
     upstreamTrust: |
       keys:
         - name: staging-recorder
           publicKey: |
             -----BEGIN PUBLIC KEY-----
             ...
             -----END PUBLIC KEY-----
           streams: ["8d1e…/staging/*"]
   ```

3. The downstream Stage naming the upstream stream and the repository that
   holds its records. The stream is the upstream Stage's `status.stream.id`.

   ```yaml
   apiVersion: delivery.pacto.trianalab.io/v1alpha1
   kind: Stage
   metadata: {name: payments, namespace: prod}
   spec:
     service: {name: payments}
     environment: prod
     applyRef: {name: payments}
     candidates:
       upstreamRecords:
         repository: registry/delivery/streams
         stream: 8d1e…/staging/payments
   ```

The promoter rebuilds what the upstream Stage verified from its records:

- A change that `Succeeded` and `Verified` there counts as verified.
- A promote that also passed the published gate there is offered here.
- A version whose change `Failed` there, of any type, is never taken.
- A version that failed its check after release there is never taken either,
  when that failure raised a rollback, because the rollback record names it.

The downstream Stage takes the newest offered version that failed in neither
cluster. Its own gates, approvals and checks still run. It sees the upstream's
history only as far as its records reach the registry, so a recent failure is
not seen yet. [What delivery does not do](delivery-limitations.md) lists the
cases.

The promoter offers nothing when it cannot check the history:

- Without `promoter.upstreamTrust` the Stage is not `Ready`, with the reason
  `UpstreamUntrusted`.
- A record no trusted key signed for that stream offers nothing, and neither
  does a stream with a gap or a fork inside it, or a registry that cannot be
  read. A promote is then refused as `NotPublished`, and the message
  says why.

The digest must also exist in the downstream Stage's own apply repository.
Use the same repository, or a mirror that keeps digests.

`pacto delivery plan` reads these records only when you pass `--trust` with
the upstream recorder's key. When a change waits for a consumer whose upstream
is in another cluster, the plan says so instead of looking for a candidate.

## External operation types

Delivery has four built-in operation types: promote, rollback, override and
unfreeze. You cannot add your own yet, such as `migrate-db` or
`rotate-secret`. The design leaves external types for later, when a team asks
for one. They would be signed OCI bundles that a cluster admin registers by
digest.
