# The Evidence Server

The Evidence Server lets places the operator cannot watch, such as CI runners
or edge sites, send signed reports about a service into your cluster. Pacto
checks each report against the contract and stores it in your registry.
*Evidence* here means a record of what a tool saw in a running system.

It is off by default (`evidence.enabled: false`). Turning it on needs three
things the chart cannot guess. Settle the first before you publish anything: it
decides which registry the contract itself has to live in.

## Check the registry serves the native Referrers API

Each report is stored next to the contract version it is about, in the same
registry repository, as an OCI 1.1 *referrer* (an artifact that points at
another artifact). Pacto has no fallback to the older tag-based scheme, so the registry holding
the contract must support Referrers lookup. **GHCR does not qualify**, and
neither does CNCF distribution (`registry:2`, `registry:3`); see
[Evidence in OCI](../../evidence.md#registry-conformance). This matters because
every other page on this site publishes to `ghcr.io`. Choose a conformant
registry for the contract before you run `pacto push`.

## Create the trust store

`pacto evidence keygen` mints an Ed25519 pair and
names the public key after the trust binding the server reads:
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
Secret key has to be the public-key filename**, which is what
`--from-file` gives you. One `--from-file` per trusted producer:

```bash
kubectl create secret generic pacto-evidence-trust \
  --namespace pacto-operator-system \
  --from-file=keys/acme-ci__release-2026.pub
```

The private `.key` stays with the producer that signs; the cluster needs only
the public key. [Evidence security](../../evidence.md#trust-store-and-trust-config) covers rotation and
multi-producer trust.

## Get the subject digest

A subject is one immutable contract revision, and
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
  short or tag-shaped reference the same way: the digest has to be all 64 hex
  characters.
- **A trust store.** `evidence.trust.existingSecret` names the Secret you
  created above. The chart does **not** check this one, so an install without
  it succeeds and the operator then exits at startup with `evidence enabled but
  no trust secret set: signature verification is mandatory`. Verification is
  mandatory.
- **A registry that serves the native Referrers API**, as above. Nothing checks
  it at install time: the chart installs, the operator starts and the failure
  surfaces later as an Evidence Server that never becomes ready.

If that registry is private, there is a fourth thing you create yourself: a
`kubernetes.io/dockerconfigjson` Secret named by
`evidence.registry.credentialsSecret`, mounted read-only as a `DOCKER_CONFIG`
directory so the server authenticates the way `pacto pull` does. Leave the value
empty for an anonymous or in-cluster registry.

See the [Helm reference](helm-reference.md) for the full value list and
[Operator configuration](operator-configuration.md) for the controller flags
each value maps to.

## Verifying the install

`evidence.enabled=true` adds a third Deployment, `pacto-evidence`, created by
the controller the same way the dashboard is:

```text
NAME              READY   UP-TO-DATE   AVAILABLE   AGE
pacto-dashboard   1/1     1            1           16s
pacto-evidence    1/1     1            1           14s
pacto-operator    1/1     1            1           21s
```

`1/1` means more than "the process started". Readiness is
`GET /api/evidence/v1/ready`, which answers `503` until **every** subject
resolves in the registry *and* answers native Referrers discovery. A
`pacto-evidence` stuck at `0/1` is nearly always a subject the cluster cannot
pull or a registry without the Referrers API. Read the server's own log:

```bash
kubectl -n pacto-operator-system logs deploy/pacto-evidence
```

The Service is `pacto-evidence` on port `8686`. Producers inside the cluster
POST signed envelopes to it:

```text
http://pacto-evidence.pacto-operator-system.svc:8686/api/evidence/v1/envelopes
```

A `pacto fleet` outside the cluster consumes the same server's read-only
contribution by **base** URL. `--evidence-url` appends
`/api/evidence/v1/targets` itself, so leave it out:

```bash
kubectl port-forward -n pacto-operator-system svc/pacto-evidence 8686:8686 &
pacto fleet search --evidence-url http://127.0.0.1:8686
```

Nothing durable lives in the cluster: no PersistentVolumeClaim, no data volume,
because the registry is the store. Delete and recreate the Deployment and the
accepted evidence is still there.
[Evidence in OCI](../../evidence.md#registry-conformance) covers what is written
and where; [the ingestion API](../../evidence.md#ingestion-http-api) lists all
five endpoints.
