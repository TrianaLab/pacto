# Pacto delivery: signed records

The recorder writes a signed record of every operation that ends, refusals
included. Records live in your registry next to the versions they touched, so
anyone can check what happened with standard tools.

The recorder is off by default. Only it holds the signing key and the right to
push to the registry. The promoter never gets either.

## What a record is

A record is an [in-toto](https://in-toto.io) statement with the predicate type
`pacto.dev/operation/v1`. It says what was asked, who asked and on whose
behalf, the Stage policy at the time, every check result with its proof, every
approval and how the operation ended. It holds references, never secrets.

The statement is signed as a DSSE envelope inside a sigstore bundle. The
recorder attaches the same bundle as an OCI referrer to four places: the
version the Stage moved to, the version it moved from, the contract of the new
version and the Stage's stream anchor. Each referrer carries the annotations
`dev.sigstore.bundle.predicateType: pacto.dev/operation/v1`,
`dev.pacto.uid`, `dev.pacto.stream` and `dev.pacto.seq`.

Every Stage has one stream. Its records are numbered (`seq`) and each names the
one before it (`parent`). The stream anchor is a small manifest in the streams
repository, tagged `stream-<sha256 of the stream id>`.

Two kinds of record stand in for an operation that cannot be recorded as is:

- **unrecordable**: a check before signing failed, for example a value that
  looks like a secret or an approval no longer bound to the operation. The
  record names the check and the intent digest and leaves the details out.
  `status.record.refused` on the Operation says why.
- **gap**: the stream skipped a number and no operation holds it.

A record can be late. It is never skipped: an Operation keeps its
`delivery.pacto.trianalab.io/record` finalizer until its record is in the
registry and listed by the Referrers API.

## Install

Your registry must support the OCI Referrers API natively. GitHub's registry
and released `distribution` do not, so records are unsupported there.
[zot](https://zotregistry.dev) works.

```yaml
recorder:
  enabled: true
  streamsRepository: registry.example/pacto/streams
  registryCredentials:
    secretName: recorder-push       # a kubernetes.io/dockerconfigjson Secret
  signing:
    ed25519:
      secretName: recorder-key      # holds key.pem, a PKCS#8 Ed25519 key
```

The push Secret needs write access to the streams repository, the repositories
your Stages apply from and the contract repositories. On kind, create the key
with `openssl genpkey -algorithm ed25519 -out key.pem` and store it with
`kubectl create secret generic recorder-key --from-file=key.pem`. In production, sign
with a KMS key (`signing.kms: awskms:///alias/pacto`) or keyless
(`signing.keyless` with the certificate `identity` and `issuer`). Both run
cosign, which the delivery image carries, and neither uploads to a public
transparency log. Give the recorder's service account the workload identity
your KMS or OIDC provider expects through `recorder.serviceAccount.annotations`.

## Check it works

Each Stage gets a `RecordStoreReady` condition:

| Reason | Meaning |
|---|---|
| `ReferrersSupported` | Records are being written |
| `ReferrersUnsupported` | The registry has no Referrers API |
| `RegistryUnavailable` | The registry or the signer did not answer; the recorder tries again |
| `StreamConflict` | The stream holds two records with one number, a record the recorder cannot verify, or the Stage names a newest record the registry does not hold. The recorder stops writing that stream until you fix it |

`status.stream.recordedSeq` and `status.stream.head` show how far the stream
is recorded, and the metric `pacto_delivery_records_pending` shows how many
operations wait for a record.

After an etcd restore the registry may be ahead of the cluster. The recorder
reads the stream from the registry, adopts records it already wrote and
continues from the newest one, so it never forks the stream.

## Limitations

- History, blame and `pacto delivery record verify` do not read records yet.
- A record signed by a key the recorder does not hold stops the stream, so
  rotating the key needs a new streams repository for now.
- The recorder checks a keyless certificate's identity and issuer, not its
  chain.
