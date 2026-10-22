# Delivery reference: signed records and key rotation

The recorder writes a signed record of every operation that ends, refusals
included, into your registry next to the versions it touched. It is off by
default. Only it holds the signing key and the right to push; the promoter
never gets either.

## What a record is

A record is an [in-toto](https://in-toto.io) statement of predicate type
`pacto.dev/operation/v1`: what was asked, who asked and for whom, the Stage
policy, every check result with its proof, every approval and the outcome. It
holds references, never secrets. It is signed as a DSSE envelope in a sigstore
bundle, attached as an OCI referrer to the version the Stage moved to, the one
it moved from, the new version's contract and the Stage's stream anchor. Each
referrer carries `dev.sigstore.bundle.predicateType: pacto.dev/operation/v1`,
`dev.pacto.uid`, `dev.pacto.stream` and `dev.pacto.seq`.

Every Stage has one stream. Its records are numbered (`seq`) and each names the
one before it (`parent`). The anchor is a manifest in the streams repository
tagged `stream-<sha256 of the stream id>`. Two kinds of record stand in for an
operation that cannot be recorded as is:

- **unrecordable**: a check before signing failed, for example a value that
  looks like a secret or an approval no longer bound to the operation. The
  record names the check and the intent digest and leaves the details out.
  `status.record.refused` on the Operation says why.
- **gap**: the stream skipped a number and no operation holds it.

A record can be late but is never skipped: an Operation keeps its
`delivery.pacto.trianalab.io/record` finalizer until the Referrers API lists
its record, and one without the finalizer is still recorded in order. A number
whose Operation is still concluding is a wait, not a gap. A Stage recreated
with the same name starts its numbers over; the old Stage's Operations are
recorded under its uid and move nothing of the new one.

## Install

Your registry must serve the OCI Referrers API natively.
[zot](https://zotregistry.dev) does; GitHub's registry and released
`distribution` do not.

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
with `openssl genpkey -algorithm ed25519 -out key.pem` and
`kubectl create secret generic recorder-key --from-file=key.pem`.

In production, sign with a KMS key (`signing.kms: awskms:///alias/pacto`): ECDSA
P-256, Ed25519 or RSA PKCS#1 v1.5 with SHA-256, never RSA-PSS, SHA-384 or
SHA-512. Or sign keyless (`signing.keyless`) with the certificate `identity`
and `issuer`, a Fulcio (`fulcioURL`), a timestamp authority (`timestampURL`)
and the `trustedRoot` that names both. The recorder shows Fulcio a projected
service account token for the `sigstore` audience. The timestamp authority is
required: the certificate lives minutes, so a record verifies only at the time
the authority saw it signed. Give the service account the workload identity
your KMS or OIDC provider expects through `recorder.serviceAccount.annotations`.

The recorder signs with the Sigstore library, never uploads to a public
transparency log and checks a probe record at startup and every record before
it writes it, so a wrong key or identity stops it before anything lands.

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

After an etcd restore the recorder adopts the records the registry already
holds and continues from the newest, so it never forks the stream.

Pacto never resolves a `StreamConflict` for you. Find the bad record with
[`pacto delivery record verify`](delivery-history.md#verify-records), delete
its referrer manifest from the streams repository, wait for `RecordStoreReady`
to turn `True`, then unfreeze the Stage. Until then every unfreeze freezes
again.

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
key is trusted only for the streams it lists, or all when the list is empty,
so a dev recorder's key cannot sign prod history. A keyless entry names
`identity`, `issuer` and the required `trustedRoot`; without it anyone could
mint a self-signed certificate with the right names. A record is trusted only
on the anchor of the stream it names.

## Recorded operations and the registry

A recorded operation is deleted after `recorder.operationTTL` (a week by
default), except the one a Stage runs, which a rollback must name. Nothing in
Pacto deletes a record, but a registry that removes old manifests removes old
records and leaves history partial. Keep the streams repository and the
referrers on your deploy and contract repositories out of retention rules.

With `recorder.cdeventsSink` set, the recorder posts a
[CDEvent](https://cdevents.dev) for every record, best effort:
`service.deployed`, `upgraded` or `rolledback` when Flux applied the new
version, and `dev.cdeventsx.pacto-operation.concluded` for every operation.
The event id is the record digest.

The limits of records are in [What delivery does not do](delivery-limitations.md#records).
