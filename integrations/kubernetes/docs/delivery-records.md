# Delivery reference: signed records and history

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

The recorder writes a signed record of every operation that ends, refusals
included, into your registry next to the versions it touched. Off by
default. Only the recorder can sign and push records. With it on, history, blame and undo read the records in place of
the Operations left in the cluster.

## What a record is

An [in-toto](https://in-toto.io) statement of predicate type
`delivery.pacto.trianalab.io/operation/v1`: what was asked, by whom and for whom, the Stage
policy, every check result with its proof, every approval and the outcome. It
holds references, never secrets. It is a sigstore bundle attached as an OCI
referrer to the version moved to, the one moved from, the new contract and the
Stage's stream anchor, or a [write-once tag](#write-once-tags). `guarded: true` says the admission policies checked who
asked and who approved. The promoter admits an operation only while its
admission policies guard the cluster, so every record it writes carries it. A
record from an earlier promoter that ran without them lacks it, and its names
are only what their writers claimed.

An eval verdict carries two signatures:

- the eval runner's, on its own answer, kept in `citation.runnerBundle`
  ([eval providers](delivery-evals.md)), with the runner named
  in `citation.runner`
- the recorder's, on the whole record around it

## Streams, stand-ins and gaps

Each Stage has one stream: records are numbered (`seq`) and each names the one
before (`parent`). An **unrecordable** record stands in for an operation that
failed a check before signing (a value that looks like a secret, an approval no
longer bound); `status.record.refused` says why. It keeps none of what the
operation did, so `record verify` reports it as a problem, and a Stage in
another cluster that follows the stream offers nothing from then on: the
stand-in could be the failure that marks a version failed. The follower's
`Ready` message names the stand-in's record id. A person checks what the
operation did upstream, then acknowledges that one stand-in on the follower:

```bash
pacto delivery propose -n prod --stage payments --type acknowledge \
  --record sha256:... --reason "checked upstream: the rollback failed nothing"
```

The acknowledge takes effect at once and is recorded like any other
operation, with who asked, the record id and the reason. The follower then
skips that stand-in and offers versions again; any other stand-in still stops
it. Only break-glass or a person who may update the follower's workload (and,
with team narrowing, of the service's team) may acknowledge, with a reason. When nothing runs on the follower
yet, or what runs names no team or cannot be read, only break-glass may
acknowledge, whatever `requireOwnerTeam` says. Break-glass can still move the
follower with an override instead. A **gap** record marks a
number no operation holds. A record can be late, never skipped: an
Operation keeps its `delivery.pacto.trianalab.io/record` finalizer until the
registry lists its record.

## Approval and intent records

- Each stream also holds an intent record for every admitted Operation and an
  approval record for every decision, outside the operation chain. See
  [signed approvals](delivery-signed-approvals.md).

## Identity pins

- When a person confirms a new publisher
  ([confirm a new signer](delivery-security.md#confirm-a-new-signer)), the
  recorder signs a `delivery.pacto.trianalab.io/identity-pin/v1` statement
  before the trust operation's own record: the service, the signer's issuer,
  exact subject and repository, who confirmed it, the approval record of
  that decision, the operation, the namespace of the Stage it was confirmed
  on and the time.
- It lives on the service's own stream `pins/<domain or ->/<name>` in the
  streams repository (the domain path-escaped, so `ghcr.io/acme` reads
  `ghcr.io%2Facme`), as a referrer or a [write-once tag](#write-once-tags)
  like every record, so anyone who reads the registry reads it.
- A pin counts only for the Stages of the namespace it names, and only when
  the approval record it cites answers an operation in that namespace, so a
  confirmation in one namespace never decides for a Stage of the same service
  in another.
- The promoter and clients use the newest pin of a namespace by signed time,
  so confirming a changed signer stops trusting the one it replaced. They use
  only a pin that verifies under the recorder's pin key and cites an approval
  record that verifies and names the same identity. Any other pin is listed
  as unreadable with why.
- To let another cluster's recorder sign pins for these services, give its
  key in `delivery.recorder.trust` an explicit `pins/` stream pattern, such
  as `pins/*/*` for every service. A pin stream has three segments, so
  `pins/*` matches none. A key with no `streams` signs no pin.

## Install

The recorder detects how your registry stores records, so any registry works:

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
Secret, so the chart refuses such a key without `allowOperatorReadableKey`
and a recorder sharing the promoter's ServiceAccount.

In production, sign with a KMS key (`delivery.recorder.signing.kms: awskms:///alias/pacto`;
ECDSA P-256, Ed25519 or RSA PKCS#1 v1.5 with SHA-256; AWS, GCP, Azure and
Vault) or keyless (`delivery.recorder.signing.keyless`: certificate `identity` and `issuer`, a
Fulcio, a required timestamp authority and a `trustedRoot`). Workload identity
goes in `delivery.recorder.serviceAccount.annotations`. The recorder checks a
probe record at startup, so a wrong key stops it first.

### Registries and their mode

| Registry | Referrers mode | Listing | Source of the row |
|---|---|---|---|
| [zot](https://zotregistry.dev) | `native` | `_catalog` | Conformance test |
| CNCF `distribution` (`registry:2`, `registry:3`) | `write-once-tags` | `_catalog` | Conformance test |
| GHCR | `write-once-tags` | tags anonymously, `_catalog` with credentials | Probe of `ghcr.io` |
| Harbor, ECR, ACR, Docker Hub | `native` where the registry serves the Referrers API | detected at run time | Registry documentation |
| GAR and any other registry | detected at run time | detected at run time | None |

- Read the mode of a running Stage with
  `kubectl get stage <name> -o jsonpath='{.status.stream.registryMode}'`.
- Before you install, `pacto fleet providers --oci <ref>` prints one
  `Registry <host>` row per registry it reads.

### Write-once tags

Without the Referrers API, each record is a tag in the streams repository:

- The tag is `stream-<hash of the stream>-<seq>-<first 12 hex of the record
  digest>`. The recorder writes it once, never moves it, and the tag names
  the record by content. Approval and intent records carry `approval` or
  `intent` in place of the seq.
- A person's signed approval is a tag of the same form in the approvals
  repository, under the Stage's anchor tag.
- The recorder never tags an apply or contract repository. A record keeps
  its referrer on a version or contract only where that registry serves the
  Referrers API. When the recorder cannot probe that registry, the write
  fails with `RegistryUnavailable` and is retried.
- Records are signed and verified the same way in both modes, and every
  reader reads both forms, so a stream keeps its history when its registry
  gains the Referrers API.
- Registries offer no conditional tag create. Tag names are
  content-addressed, so two writers never race for one tag, and a taken tag
  that names another record is refused.
- Anyone with push access can add, move or delete a tag, as they can attach
  a referrer. Every loss fails safe. A tag no trusted key signed is ignored and a deleted record
  breaks the chain from `status.stream.head`, which readers report. A person's
  signed approval record that was deleted does not count, so the person
  approves again. A record the recorder signed for an Approval made in the
  cluster counts once written; if it is deleted later, the operation record
  names a record id nobody can read. A reader
  resolves each tag once, so a moved tag is reported as unreadable the next
  time a new reader (a CLI call or a restarted process) resolves it.
- Tag-immutability rules (Harbor, ECR, ACR and GAR) can enforce write-once
  on the streams repository.
- `pacto delivery plan` reads only this Stage's records for what a version
  did elsewhere, so that answer stays partial.
- A Stage recorded `native` never moves to tags on its own. If its registry
  stops serving the Referrers API, `RecordStoreReady` turns `False` with
  reason `RegistryUnavailable` and the message `the registry served the
  Referrers API for this stream and no longer does`. The records admission
  policy refuses changing a recorded `native` mode for every requester, the
  recorder included.

## Check it works

Each Stage gets a `RecordStoreReady` condition:

| Reason | Meaning |
|---|---|
| `ReferrersSupported` | Records are being written as native referrers |
| `WriteOnceTags` | Records are being written as write-once tags: the registry serves no Referrers API |
| `RegistryUnavailable` | The registry did not answer; it tries again |
| `SignerUnavailable` | The signer (KMS key, keyless token, Fulcio or timestamp authority) failed; it tries again |
| `StreamConflict` | Two trusted records with one number, a broken chain or a missing head (the one the Stage names). The recorder stops and the Stage freezes |

A record signed by no key trusted for its stream is not part of its history:
the recorder and followers skip it, and the condition names the first
one. Anyone who can push can attach a referrer, so an untrusted one never
freezes a Stage.

Pacto never resolves a `StreamConflict` for you. Find the bad record with
`pacto delivery record verify`, delete its referrer manifest (or its tag)
from the streams repository, wait for `RecordStoreReady` to turn `True`, then unfreeze. After an
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
pacto delivery history --all --correlation-id inc-4211 --since 24h
pacto delivery blame -n prod --stage payments
pacto delivery undo -n prod <operation>
```

Every answer says when it was read (`asOf`), its `source` (`records`, or
`status` when only the cluster's Operations could be read) and its
`completeness`: `complete` only when every record verifies and the chain is
whole. `problems` lists what the records show is wrong, and `unreadable` what
could not be read. On a partial answer a missing change may still have
happened. `--all` walks every Stage you can list, up to 500 in two minutes.

`--cause` keeps the changes whose cause (reason, operation or cited verdict)
names a text, ignoring case, and `--correlation-id` those with exactly that
id; both narrow the timeline too. Each line shows when it was applied, its
cause and its correlation id. A line with record `-` is read from the
Operation's status, so those three are not signed, and the output says so.
`pacto delivery record get` reads one record by its id; see
[status, progress and lifecycle](delivery-status.md).

[A service's changes and history](../../service-changes.md#from-the-cli-and-mcp)
explains `--at` and the `IMPACT` and `REVISION (DERIVED)` columns.

`blame` names the change that moved the Stage to what it runs: who asked, who
approved, what each check said. No change on a partial history means unknown,
never nobody. `undo` proposes nothing; it returns the plan and the command that
would undo an operation. `pacto delivery plan` also lists what the new
version's records say happened on other Stages, so a version that failed in
test shows when you plan it for prod.

### Correlation ids and the person an agent acts for

Records sign `correlationID` and `onBehalfOf` as the Operation's intent said
them:

- A plan or a proposal takes a correlation id from `--correlation-id`,
  `correlation_id` or the dashboard's "Ticket or incident id" field. The
  automatic rollback, freeze and step-down the promoter raises after a change
  inherit its id.
- The admission policy accepts a `spec.correlationID` of at most 128
  printable ASCII characters, so every reader prints it as written.
- `onBehalfOf` is asserted, never verified; an agent must set it when it
  proposes ([agents](delivery-agents.md)).
- The intent digest binds the cause a proposal cites, never the correlation
  id: a new id never makes a reviewed plan stale.

### Upgrade: cause in the intent digest

- Proposals that cite a cause (`--cause-verdict`, `cause_verdict`) need the
  CLI, the dashboard and the operator at the same release, in both
  directions. Across releases the client and the promoter compute different
  digests, and the promoter finishes the Operation `Rejected` with
  `PlanStale`. Planning again with the same client gives the same digest, so
  upgrade the side that is behind.
- Proposals without a cause and Operations held before the upgrade are
  unaffected.

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
  runner must sign, so you decide which verdicts are exams.
- A runner key verifies only runner answers, and a recorder key only records.
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

[Check one record without Pacto](../../verify-a-record.md#check-one-record-without-pacto)
gives the oras and cosign commands for a recorder key file.

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

## Retention and events

A recorded Operation is deleted after `delivery.recorder.operationTTL` (a week), except
the one a Stage runs. Nothing in Pacto deletes a record, but registry retention
rules can, leaving history partial: keep the streams repository and the
referrers out of them. With `delivery.recorder.cdeventsSink` set, the recorder posts a
[CDEvent](https://cdevents.dev) for every record, best effort. It also posts one
when a Stage is created or deleted:

| Event | Sent when | `time` |
|-------|-----------|--------|
| `environment.created` | the promoter first gives the Stage its stream id | the Stage's `creationTimestamp` |
| `environment.deleted` | the Stage is deleted after it got its stream id | its `deletionTimestamp`; absent when the deleted Stage has none, with `customData.observedAt` holding when the recorder saw the delete (CDEvents requires `context.timestamp`, so it holds that time too) |

About these two events:

- The CloudEvent id is `<stage uid>/created` or `<stage uid>/deleted`, so a
  sink can deduplicate.
- The subject id is the Stage's stream id, unique per Stage, and its
  `content.name` is the environment (the CDEvents environment content holds
  only `name` and `uri`, so `service` and `domain` go in `customData`).
- `customData` carries `service`, `domain`, `stage`, `stageUID`, `createdAt`
  and, on a delete with no `deletionTimestamp`, `observedAt`.
- A recorder restart sends neither again. A Stage whose status was reset sends
  `environment.created` again with its original `createdAt`.
- Each event names one Stage; other Stages for the same environment are
  unaffected.
- Every event's `data` follows the CDEvents v0.5.1 schemas. Any event whose
  occurrence time is unknown has no CloudEvent `time`, and its
  `context.timestamp` and `customData.observedAt` hold when the recorder saw it.

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
