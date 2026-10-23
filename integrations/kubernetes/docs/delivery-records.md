# Delivery reference: signed records and history

The recorder writes a signed record of every operation that ends, refusals
included, into your registry next to the versions it touched. It is off by
default. Only it holds the signing key and the right to push; the promoter
never gets either. With it on, history, blame and undo read the records, not
the Operations left in the cluster.

## What a record is

An [in-toto](https://in-toto.io) statement of predicate type
`pacto.dev/operation/v1`: what was asked, by whom and for whom, the Stage
policy, every check result with its proof, every approval and the outcome. It
holds references, never secrets. It is a sigstore bundle attached as an OCI
referrer to the version moved to, the one moved from, the new contract and the
Stage's stream anchor.

Each Stage has one stream: records are numbered (`seq`) and each names the one
before (`parent`). An **unrecordable** record stands in for an operation that
failed a check before signing (a value that looks like a secret, an approval no
longer bound); `status.record.refused` says why. A **gap** record marks a
number no operation holds. A record can be late but is never skipped: an
Operation keeps its `delivery.pacto.trianalab.io/record` finalizer until the
registry lists its record.

## Install

Your registry must serve the OCI Referrers API natively.
[zot](https://zotregistry.dev) does; GitHub's registry and released
`distribution` do not, so they hold no records.

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

The push Secret needs write access to the streams repository and the deploy
and contract repositories. Create a test key with
`openssl genpkey -algorithm ed25519 -out key.pem`.

In production, sign with a KMS key (`signing.kms: awskms:///alias/pacto`;
ECDSA P-256, Ed25519 or RSA PKCS#1 v1.5 with SHA-256; AWS, GCP, Azure and
Vault) or keyless (`signing.keyless`: certificate `identity` and `issuer`, a
Fulcio, a required timestamp authority and a `trustedRoot`). Workload identity
goes in `recorder.serviceAccount.annotations`. The recorder never uploads to a
public transparency log and checks a probe record at startup, so a wrong key
stops it before anything lands.

## Check it works

Each Stage gets a `RecordStoreReady` condition:

| Reason | Meaning |
|---|---|
| `ReferrersSupported` | Records are being written |
| `ReferrersUnsupported` | The registry has no Referrers API |
| `RegistryUnavailable` | The registry or signer did not answer; it tries again |
| `StreamConflict` | Two records with one number, a broken chain or an untrusted signature. The recorder stops and the Stage freezes |

Pacto never resolves a `StreamConflict` for you. Find the bad record with
`pacto delivery record verify`, delete its referrer manifest from the streams
repository, wait for `RecordStoreReady` to turn `True`, then unfreeze. After an
etcd restore the recorder continues from the newest record, never forking.

## Trust and key rotation

Readers and the recorder check every signature against a trust config. To
rotate, sign with the new key and keep the old one listed:

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

`streams` are patterns over stream ids (`<cluster>/<namespace>/<stage>`), so a
dev key cannot sign prod history. A keyless entry names `identity`, `issuer`
and the required `trustedRoot`. Pass the same file (or the recorder's PEM
public key) to the CLI with `--trust` or `PACTO_DELIVERY_TRUST`, and to
`pacto mcp --delivery` and `pacto dashboard` with `--delivery-trust`. Without
it, records are read unchecked and every answer says it is partial.

## History, blame and undo

```bash
pacto delivery history -n prod --stage payments --trust recorder.pub
pacto delivery history --all --trust recorder.pub
pacto delivery blame -n prod --stage payments
pacto delivery undo -n prod <operation>
```

Every answer says when it was read (`asOf`), its `source` (`records`, or
`status` when only the cluster's Operations could be read) and its
`completeness`: `complete` only when every record verifies and the chain is
whole. `problems` lists what the records show is wrong, and `unreadable` what
could not be read. On a partial answer a missing change may still have
happened. `--all` walks every Stage you can list, up to 500 in two minutes.

`blame` names the change that moved the Stage to what it runs: who asked, who
approved, what each check said. No change on a partial history means unknown,
never nobody. `undo` proposes nothing; it returns the plan and the command that
would undo an operation. `pacto delivery plan` also lists what the new
version's records say happened on other Stages, so a version that failed in
test shows when you plan it for prod.

## Verify records

```bash
pacto delivery record verify -n prod --stage payments --trust recorder.pub
```

It checks every signature and the whole chain, exits non-zero if anything is
wrong and prints an [oras](https://oras.land) and
[cosign](https://github.com/sigstore/cosign) command that checks one record
without Pacto. `record verify --all` checks every stream.

## Retention and events

A recorded Operation is deleted after `recorder.operationTTL` (a week), except
the one a Stage runs. Nothing in Pacto deletes a record, but registry retention
rules can, leaving history partial: keep the streams repository and the
referrers out of them. With `recorder.cdeventsSink` set, the recorder posts a
[CDEvent](https://cdevents.dev) for every record, best effort.

## Limits

- The recorder notices a tampered record only when it next records on that
  Stage; `record verify` checks any time.
- Deleting a Stage while a change runs leaves that change unrecorded. Wait for
  its changes to conclude first.
- Without Rekor a keyless record has no signed time, so its chain is checked at
  the moment the certificate was issued.
