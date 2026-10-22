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
registry and listed by the Referrers API. An Operation without the finalizer,
because admission is off or someone removed it, is still recorded in order.
A number the Stage gave an Operation that has not finished concluding is a
wait, not a gap. A Stage deleted and recreated with the same name starts its
numbers over. An Operation the old Stage concluded is still recorded, under
the old Stage's uid, and moves nothing of the new Stage.

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
with a KMS key (`signing.kms: awskms:///alias/pacto`) or keyless. The
recorder signs with the Sigstore library, with no cosign binary, and never
uploads to a public transparency log. Keyless signing (`signing.keyless`)
needs the certificate `identity` and `issuer`, a Fulcio (`fulcioURL`), a
timestamp authority (`timestampURL`) and `trustedRoot`, the Sigstore
`trusted_root.json` that names both. The recorder presents a projected
service account token for the `sigstore` audience to Fulcio. The timestamp
authority is required: a keyless certificate lives minutes, so a record
verifies only at the time the authority saw it signed. Give the recorder's service account the workload identity
your KMS or OIDC provider expects through `recorder.serviceAccount.annotations`.

## Check it works

Each Stage gets a `RecordStoreReady` condition:

| Reason | Meaning |
|---|---|
| `ReferrersSupported` | Records are being written |
| `ReferrersUnsupported` | The registry has no Referrers API |
| `RegistryUnavailable` | The registry or the signer did not answer; the recorder tries again |
| `StreamConflict` | The stream holds two records with one number, a record that does not chain from the one before, a record no trusted key signed or the Stage names a newest record the registry does not hold. The recorder stops writing that stream and the promoter freezes the Stage |

`status.stream.recordedSeq` and `status.stream.head` show how far the stream
is recorded, and the metric `pacto_delivery_records_pending` shows how many
operations wait for a record.

After an etcd restore the registry may be ahead of the cluster. The recorder
reads the stream from the registry, adopts records it already wrote and
continues from the newest one, so it never forks the stream.

A `StreamConflict` is never resolved for you. Find the bad record with
`pacto delivery record verify` (see [reading records](delivery-history.md)),
delete its referrer manifest from the streams repository, wait for
`RecordStoreReady` to turn `True` and then unfreeze the Stage. While the
conflict lasts, the promoter freezes the Stage again after every unfreeze.

## Trust and key rotation

Every record names, by digest, the trust config the recorder checked the
stream with. The recorder trusts its own key. To rotate keys, sign with the
new key and list the old one in `recorder.trust`, so the old records still
verify:

```yaml
recorder:
  trust: |
    keys:
      - name: old-recorder
        publicKey: |
          -----BEGIN PUBLIC KEY-----
          ...
          -----END PUBLIC KEY-----
        streams: ["*/prod/*"]
```

`streams` are patterns over stream ids (`<cluster>/<namespace>/<stage>`). A
key is trusted only for the streams it lists, or for all of them when the list
is empty, so a dev recorder's key cannot sign prod history. A keyless entry
names `identity`, `issuer` and `trustedRoot` (the Sigstore `trusted_root.json`
of its Fulcio and timestamp authority). The trusted root is required: without
it anyone could mint a self-signed certificate that names the right identity
and issuer.

A record is trusted only on the anchor of the stream it names. A validly
signed record of another stream attached to this one's anchor reads as one
that could not be verified.

## Recorded operations and the registry

Once recorded, an operation is deleted after `recorder.operationTTL` (a week
by default). The operation a Stage runs is kept, because a rollback must name
it. Nothing in Pacto deletes a record. If your registry removes old manifests,
it removes old records too, and history from then on is partial. Keep the
streams repository and the referrers on your deploy and contract repositories
out of any retention rule.

With `recorder.cdeventsSink` set, the recorder posts a
[CDEvent](https://cdevents.dev) for every record, best effort:
`service.deployed`, `upgraded` or `rolledback` when Flux applied the new
version, and `dev.cdeventsx.pacto-operation.concluded` for every operation.
The event id is the record digest.

## Limitations

- A keyless record is checked against its certificate's identity, issuer and
  chain. With Rekor off there is no signed time, so the chain is checked at the
  moment the certificate was issued.
- Deleting a Stage while one of its changes is running ends that change
  `Errored` with no stream number, so it gets no record even though it moved
  the Stage. Wait for a Stage's changes to conclude before you delete it.
- The recorder detects that a record was tampered with or the chain broken
  only when it next has an operation to record on that Stage.
  `pacto delivery record verify` checks any time.
