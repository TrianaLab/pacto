# Delivery reference: signed records and history

The recorder writes a signed record of every operation that ends, refusals
included, into your registry next to the versions it touched. Off by
default. Only it can sign and push; the promoter can do neither. With it on, history, blame and undo read the records, not
the Operations left in the cluster.

## What a record is

An [in-toto](https://in-toto.io) statement of predicate type
`delivery.pacto.trianalab.io/operation/v1`: what was asked, by whom and for whom, the Stage
policy, every check result with its proof, every approval and the outcome. It
holds references, never secrets. It is a sigstore bundle attached as an OCI
referrer to the version moved to, the one moved from, the new contract and the
Stage's stream anchor. `guarded: true` says the admission policies checked who
asked and who approved. A record without it was written while the chart ran
without them (`delivery.admissionPolicies.allowUnguarded`), so those names are only what
their writers claimed.

An eval verdict carries two signatures:

- the eval runner's, on its own answer, kept in `citation.runnerBundle`
  ([eval providers](delivery-evals.md)), with the runner named
  in `citation.runner`
- the recorder's, on the whole record around it

Each Stage has one stream: records are numbered (`seq`) and each names the one
before (`parent`). An **unrecordable** record stands in for an operation that
failed a check before signing (a value that looks like a secret, an approval no
longer bound); `status.record.refused` says why. It keeps none of what the
operation did, so `record verify` reports it as a problem, and a Stage in
another cluster that follows the stream offers nothing from then on: the
stand-in could be the failure that marks a version failed. The follower's
`Ready` message names the stand-in's record id. A person of the team that owns
the service checks what the operation did upstream, then acknowledges that one
stand-in on the follower:

```bash
pacto delivery propose -n prod --stage payments --type acknowledge \
  --record sha256:... --reason "checked upstream: the rollback failed nothing"
```

The acknowledge takes effect at once and is recorded like any other
operation, with who asked, the record id and the reason. The follower then
skips that stand-in and offers versions again; any other stand-in still stops
it. Only a person of that team or break-glass may acknowledge, never an agent
or the promoter, and a reason is required. Break-glass can still move the
follower with an override instead. A **gap** record marks a
number no operation holds. A record can be late but is never skipped: an
Operation keeps its `delivery.pacto.trianalab.io/record` finalizer until the
registry lists its record.

## Install

Your registry must serve the OCI Referrers API natively.
[zot](https://zotregistry.dev) does; GitHub's registry and released
`distribution` do not, so they hold no records.

```yaml
delivery:
  recorder:
    enabled: true
    streamsRepository: registry.example/pacto/streams
    registryCredentials:
      secretName: recorder-push       # a kubernetes.io/dockerconfigjson Secret
    signing:
      ed25519:
        secretName: recorder-key      # holds key.pem, a PKCS#8 Ed25519 key
      allowOperatorReadableKey: true  # the operator can read this Secret too
```

The push Secret needs write access to the streams, deploy and contract
repositories. Create a test key with
`openssl genpkey -algorithm ed25519 -out key.pem`. The operator reads every
Secret, so the chart refuses such a key without `allowOperatorReadableKey`,
and refuses a recorder that shares the promoter's ServiceAccount.

In production, sign with a KMS key (`delivery.recorder.signing.kms: awskms:///alias/pacto`;
ECDSA P-256, Ed25519 or RSA PKCS#1 v1.5 with SHA-256; AWS, GCP, Azure and
Vault) or keyless (`delivery.recorder.signing.keyless`: certificate `identity` and `issuer`, a
Fulcio, a required timestamp authority and a `trustedRoot`). Workload identity
goes in `delivery.recorder.serviceAccount.annotations`. The recorder checks a
probe record at startup, so a wrong key stops it before anything lands.

## Check it works

Each Stage gets a `RecordStoreReady` condition:

| Reason | Meaning |
|---|---|
| `ReferrersSupported` | Records are being written |
| `ReferrersUnsupported` | The registry has no Referrers API |
| `RegistryUnavailable` | The registry did not answer; it tries again |
| `SignerUnavailable` | The signer (KMS key, keyless token, Fulcio or timestamp authority) failed; it tries again |
| `StreamConflict` | Two trusted records with one number, a broken chain or a missing head (the one the Stage names). The recorder stops and the Stage freezes |

A record signed by no key trusted for its stream is not part of its history:
the recorder and followers skip it, and the condition names the first
one. Anyone who can push can attach a referrer, so an untrusted one never
freezes a Stage.

Pacto never resolves a `StreamConflict` for you. Find the bad record with
`pacto delivery record verify`, delete its referrer manifest from the streams
repository, wait for `RecordStoreReady` to turn `True`, then unfreeze. After an
etcd restore the recorder continues from the newest record.

## Require records

By default automation keeps moving a Stage while the recorder cannot write,
and records catch up later. To stop that, set `records.required`:

```yaml
spec:
  records:
    required: true
```

Then every promote or rollback an agent or the promoter raises waits for a
person's approval while `RecordStoreReady` is not `True`, or while more than
five of the Stage's operations wait for a record. People, break-glass and
freezes are not held. With `delivery.recorder.monitoring.enabled`, the chart scrapes the
recorder and ships a `PrometheusRule`. `PactoDeliveryRecordsPending` fires when
a stream has waited 15 minutes for a record, and `PactoDeliveryRecorderDown`
when no recorder answers a scrape.

## Trust, key rotation and revocation

Readers and the recorder check every signature against a trust config. How
to write it, rotate the recorder's key and revoke a leaked one is in
[Delivery reference: record keys](delivery-record-keys.md).

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
wrong and prints a command that checks one record without Pacto, with
[oras](https://oras.land) and [cosign](https://github.com/sigstore/cosign).
`record verify --all` checks every stream.

Eval runners:

- A record with an eval runner's signed answer verifies only when the trust
  config also lists that runner's key with `role: runner`. A keyless runner
  takes `identity`, `issuer` and `trustedRoot`.
- The output names who ran each exam.
- An eval verdict that says Pass or Fail fails the check when it keeps no
  runner-signed answer, when the answer is for another operation or version,
  or when the signed counts and the Stage's `minPassRate` give another result.
- `providers` on a runner key lists the gate providers whose verdicts a
  runner must sign, so you decide which verdicts are exams, not the promoter.
- A runner key never verifies a record, nor a recorder key a runner's answer.
- To check a runner's signature with cosign, see
  [Verify a record](../../verify-a-record.md#check-who-ran-an-exam).

```yaml
keys:
  - name: recorder
    publicKey: |
      -----BEGIN PUBLIC KEY-----
      ...
  - name: evals-ci
    role: runner
    providers: [evals]
    publicKey: |
      -----BEGIN PUBLIC KEY-----
      ...
```

```text
  seq 4    operation     sha256:9c1f... promote-pay-agent-1-1-0
           evals: the exam was run and signed by evals-ci
```

To check one record without Pacto, for example:

```bash
REPO=registry.example/pacto/streams
oras blob fetch --output record.json \
  "$REPO@$(oras manifest fetch "$REPO@<record manifest>" | jq -r '.layers[0].digest')"
cosign verify-blob-attestation --bundle record.json --key recorder.pub \
  --type delivery.pacto.trianalab.io/operation/v1 --insecure-ignore-tlog \
  --digest <anchor digest without sha256:> --digestAlg sha256
```

For a keyless recorder it uses these flags instead of `--key recorder.pub`,
with the entry's `trustedRoot` saved as `trusted_root.json`:

```bash
--trusted-root trusted_root.json --certificate-identity <identity> \
  --certificate-oidc-issuer <issuer> --use-signed-timestamps \
  --insecure-ignore-sct
```

`--insecure-ignore-sct` appears only when the trusted root lists no
certificate transparency log, as in many private Sigstores: then a
certificate carries no SCT and cosign needs the flag. The TSA timestamp is
still checked.

`oras discover` on a version lists the records attached to it. The record
itself is a DSSE envelope: `jq -r .dsseEnvelope.payload record.json | base64 -d`
prints the statement.

## Retention and events

A recorded Operation is deleted after `delivery.recorder.operationTTL` (a week), except
the one a Stage runs. Nothing in Pacto deletes a record, but registry retention
rules can, leaving history partial: keep the streams repository and the
referrers out of them. With `delivery.recorder.cdeventsSink` set, the recorder posts a
[CDEvent](https://cdevents.dev) for every record, best effort.

## Limits

- The recorder notices a tampered record only when it next records on that
  Stage; `record verify` checks any time.
- Deleting a Stage while a change runs leaves that change unrecorded. Wait for
  its changes to conclude first.
- A follower in another cluster notices records removed from a stream's tail
  only against the newest record it already read
  ([details](delivery-reach.md#when-the-records-cannot-be-trusted)).
- No record goes to a transparency log such as Rekor. A keyless record's signed time comes
  from the timestamp authority the recorder must name, and the certificate is
  checked at that time.
