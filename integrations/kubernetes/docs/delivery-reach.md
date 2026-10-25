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
- have one OCI source (`spec.source.repoURL: oci://…`), not `spec.sources`, and
  set no Kustomize images, patches or components, Helm parameters, values or
  value files, or plugin. Otherwise the Stage's `Mediated` condition is
  `False`: something besides the digest decides what runs.
- sync on its own (`syncPolicy.automated`), because the promoter never runs a sync.
- carry the `delivery.pacto.trianalab.io/apply-ref` label. Without it the Stage
  has `Ready` `False` with the reason `ApplyRefUnlabelled`.
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

The promoter sets `spec.source.targetRevision` to the new digest and asks
Argo CD to refresh. The move counts as applied once the Application is `Synced`
and `Healthy` at that digest; `status.sync.revision` alone is not proof, since
it moves on refresh while the old version still runs. A sync of the digest that
ends `Failed` or `Error`, or `Degraded` health after that sync, fails the
promote and starts the usual rollback.

Argo CD does not check signatures, but the candidate source still does, and the
promoter only ever writes a digest, so Argo CD fetches exactly the checked
bytes. Admission keeps the rest of `spec.source`, `spec.sources` and the label
to break-glass, so nobody else changes what those bytes become. An Argo CD Stage places no consumers, so label the Pactos it runs with
`delivery.pacto.trianalab.io/environment`.

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

The promoter rebuilds from the records what the upstream verified: a promote
that `Succeeded`, `Verified` and passed the published gate there is offered
here, and a version that failed there (or was rolled back after release) is
never taken. That includes a version that fails a check after release when
the promoter cannot roll it back there (a person or agent chose it, or its
rollback is used up): the promoter freezes that Stage with a freeze operation
whose record names the failed version. Its own gates, approvals and checks still run. The digest must
also exist in the Stage's own apply repository: use the same repository or a
mirror that keeps digests. `pacto delivery plan` reads these records only with
`--trust` and the upstream recorder's key.

The promoter offers nothing when it cannot check the history: without
`promoter.upstreamTrust` the Stage is not `Ready` (`UpstreamUntrusted`), and an
untrusted record, a gap, a fork or an unreadable registry refuse a promote as
`NotPublished`, saying why.

## Limits

- A Stage sees the upstream's history only as far as its records reach the
  registry, so a failure not yet recorded, or a stream cut at its newest
  records, is not seen.
