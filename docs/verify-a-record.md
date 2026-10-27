# Verify a record

This page is for an auditor or security reviewer who must check what Pacto
did, using standard tools only. It shows how to read a
record, check its signature, check the whole history and check that a
released version was signed. Without access to the cluster you still take
three things from the team that runs Pacto: the public key records are
checked against, the address where a Stage's records hang and the id of its
newest record (a hash of that record, explained in
[Check the whole history](#check-the-whole-history)). Ask for all three in
writing and keep them: a later mismatch is evidence. Two optional checks
below need one more key each: the eval runner's for exams and the publisher's
for released versions.
The full detail is in [Signed records and history](integrations/kubernetes/delivery-records.md).

## The words on this page

Pacto decides which version of each service runs in each environment, such
as test or prod. Each service has a **contract**, a file called `pacto.yaml`
that says who owns it and which other services it calls.

| Word | Meaning |
|---|---|
| Stage | One service in one environment, for example payments in prod |
| Change | One request to move a Stage to another version, from the request to its end. In Kubernetes it is an object of kind `Operation` |
| Record | A signed account of one finished change |
| Recorder | The part of Pacto that writes and signs records. It is the only part that holds the signing key |
| Promoter | The part of Pacto that plans each change and moves the version. It does not hold the recorder's key |
| Registry | The container registry where your images live, such as zot or Harbor |
| Digest | The `sha256:...` fingerprint of one exact image or file in a registry |
| Flux | The tool that makes a Kubernetes cluster run the files you give it. Pacto tells it which version to run |
| Admission checks | Rules the Kubernetes cluster runs on every request, before it accepts it. Pacto's check who may ask for and approve a change |
| Referrer | A file a registry stores "attached" to a digest, found by asking the registry what points at that digest |

## What a record says

A record answers: what was asked, by whom, for which Stage, what each check
said and why, who approved and how it ended. Refusals are recorded too. It
holds references, never secrets. A record is an
[in-toto](https://in-toto.io) statement, a standard format for signed claims
about software, with the type `delivery.pacto.trianalab.io/operation/v1`.

Every field sits under `.predicate`:

| Field | What it says |
|---|---|
| `kind` | `operation` for a change, `gap` for a placeholder (see below), `unrecordable` for a signed stand-in for a change whose own record failed a check. A stand-in takes that change's number in the chain and hangs on the anchor only. A Stage in another cluster that follows the stream stops offering at a stand-in until a person acknowledges it (an `acknowledge` record on that Stage names its record id in `intent.params.record`) |
| `intent` | What was asked: the change's name, its Stage (`intent.stage.namespace` and `intent.stage.name`) and its reason |
| `actor.user`, `actor.class` | Who asked, and whether that is a `human`, an `agent`, `break-glass` (a member of the emergency group, whose change skips every check and approval and freezes the Stage) or Pacto itself (`controller`) |
| `actor.onBehalfOf` | The person an agent said it acted for. Only claimed, never checked, and marked so |
| `approvals[]` | Who answered (`by`), the answer (`decision`: `approve`, `reject` or `accept-unknown`, which means a person let the change go ahead although a check said Unknown) and when they last signed in (`authTime`) |
| `verdicts[]` | Each check (`gate`), when it ran (`phase`: `pre` before the move, `verify` after) and its `result`: `Pass`, `Fail` or `Unknown` |
| `verdicts[].citation.runner`, `.runnerBundle` | For an eval check: who ran the exam, and the answer that runner signed itself (see "Check who ran an exam") |
| `outcome.result` | How it ended, such as `Succeeded`, `Failed`, `Rejected` or `Cancelled` |
| `outcome.pointer.before`, `.after` | The version digest the Stage ran before and after |
| `guarded` | Read this first. `true` when the cluster's admission checks confirmed who asked and who approved. Without it, those names are only what the writer claimed |
| `chain.seq`, `chain.parent` | The record's number in its Stage's history and the id of the record before it (see "Check the whole history") |

Records exist only when your platform team turns on the recorder. They need
a registry that supports referrers, such as zot. GitHub's registry does not
support them.

## Where records live

Each record is stored in your registry as a referrer, attached to the version
the change moved to, the version it moved from, the new contract and one
fixed digest per Stage called the **anchor**. These four digests are the
record's signed subjects (`.subject[]`, named `to`, `from`, `contract` and
`anchor`). The anchor is where the whole history of one Stage hangs. All
anchors live in one repository, the **streams repository**. The anchor's full
address is in the Stage:

```bash
kubectl get stages.delivery.pacto.trianalab.io -n prod payments -o jsonpath='{.status.stream.anchor}'
# registry.example/pacto/streams@sha256:3a7f...
```

If you have no cluster access, ask the platform team for this address. It
never changes for a Stage.

## Get the public key

You check signatures against the recorder's public key, a file such as
`recorder.pub`. Get it from the platform team, not from the registry, so a
forger cannot hand you their own. The platform team makes it with one of
these (you need neither):

```bash
openssl pkey -in key.pem -pubout -out recorder.pub          # from a key file
cosign public-key --key awskms:///alias/pacto > recorder.pub # from a cloud KMS key
```

If the recorder signs with no key file (keyless), ask instead for its
certificate identity, issuer and trusted root; the
[records reference](integrations/kubernetes/delivery-records.md#verify-records)
shows the flags.

## Check one record without Pacto

You need [oras](https://oras.land), [cosign](https://github.com/sigstore/cosign),
`jq` and `base64`, and read access to the registry (`oras login registry.example`).
List the records on the anchor, fetch one and check its signature. Records
are the entries of type `application/vnd.dev.sigstore.bundle.v0.3+json`;
`--artifact-type` lists only those:

```bash
ANCHOR=registry.example/pacto/streams@sha256:3a7f...   # from the step above
oras discover --artifact-type application/vnd.dev.sigstore.bundle.v0.3+json "$ANCHOR"
REC=sha256:...                                         # the digest of one entry in the list (not the record id below)
REPO=${ANCHOR%@*}
oras blob fetch --output record.json \
  "$REPO@$(oras manifest fetch "$REPO@$REC" | jq -r '.layers[0].digest')"
cosign verify-blob-attestation --bundle record.json --key recorder.pub \
  --type delivery.pacto.trianalab.io/operation/v1 --insecure-ignore-tlog \
  --digest "${ANCHOR##*sha256:}" --digestAlg sha256
jq -r .dsseEnvelope.payload record.json | base64 -d | jq .predicate   # read it
```

`--digest` is the anchor's digest: the check passes only if this record names
this Stage's anchor among its signed subjects, so a record moved from another
Stage fails. cosign prints `Verified OK` when the signature holds and exits non-zero when
it does not. `--insecure-ignore-tlog` is expected: the recorder never writes to a public
log, because records name your services. The signature alone proves the
record is unchanged.

## Check the whole history

Each Stage's records are numbered 1, 2, 3 and so on (`.predicate.chain.seq`).
A record's **id** is `sha256:` followed by the SHA-256 of its signed
statement, the decoded payload:

```bash
jq -r .dsseEnvelope.payload record.json | base64 -d | shasum -a 256   # the id, without "sha256:"
```

`oras discover` lists a different digest for the entry. That digest names
the stored file, and the id names what was signed. Compare ids with ids.

Fetch and check every record on the anchor as above, then check three
things:

1. The numbers run 1, 2, 3 with no hole and no number twice.
2. Each record's `.predicate.chain.parent` is the id of the record numbered
   one lower. Record 1 has no parent.
3. The newest record's id equals the Stage's `.status.stream.head`, which the
   platform team can read for you. If it does not, newer records are missing.

A record with `.predicate.kind` `gap` is a signed placeholder for a number no
change used. It fills its number, so it is not a hole, but every gap is worth
asking the platform team about.
With cluster access, Pacto does the same check in one command and exits
non-zero if anything is wrong:

```bash
pacto delivery record verify -n prod --stage payments --trust recorder.pub
pacto delivery blame -n prod --stage payments --trust recorder.pub   # who asked and approved what runs now
```

When Pacto cannot read every record, its answer says `partial`. Then a
missing change may still have happened: no answer means "unknown", never
"nobody".

## Check who ran an exam

An eval runner runs each eval check, such as an AI agent's eval suite. The
runner signs its own answer: which version it ran,
the sha256 of the suite, how many cases passed and where the run can be read
again. The record keeps that signed answer in the verdict's
`citation.runnerBundle`, inside the recorder's signature. So you can check
the runner's signature yourself, without trusting the recorder or the
promoter. Get the runner's public key from the team that runs the evals,
not from the record or the registry:

```bash
jq -r .dsseEnvelope.payload record.json | base64 -d \
  | jq -r '.predicate.verdicts[] | select(.citation.runnerBundle) | .citation.runnerBundle' > runner.json
VERSION=$(jq -r .dsseEnvelope.payload record.json | base64 -d | jq -r '.subject[] | select(.name=="to") | .digest.sha256')
cosign verify-blob-attestation --bundle runner.json --key runner.pub \
  --type delivery.pacto.trianalab.io/eval/v1 --insecure-ignore-tlog \
  --digest "$VERSION" --digestAlg sha256
jq -r .dsseEnvelope.payload runner.json | base64 -d | jq .predicate   # what it signed
```

The commands take the first eval check of the record; a record with more
than one keeps one signed answer per check. Then check what the runner
signed against the record:

- `operation` is the record's `.predicate.intent.uid` and the subject its `to` digest
- the counts and the suite match the verdict's `citation`
- the verdict's result follows from the counts and the Stage check's
  `minPassRate` in the record's `.predicate.policy` (a model the runner named can only
  make it worse)

An eval verdict that says Pass or Fail with no `runnerBundle` has no runner
signature. Do not trust it. With cluster access,
`pacto delivery record verify --trust keys.yaml` does all of this when that
file lists the runner's key with `role: runner` beside the recorder's, and prints who ran each exam. It
finds the eval verdicts from the record's Stage policy and citations. To
decide that yourself, list on the runner key the providers whose verdicts it
must sign:

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

What this cannot catch: a promoter that reuses an answer the runner signed
earlier in the same operation, for the same version. Each answer carries the
one-time value the promoter asked with, and only the promoter knows which one
it used last.

## Check a version was signed

Every version Pacto promotes was published and signed with `pacto publish`.
A break-glass override can skip this: look for `actor.class` `break-glass`
in the records and check its `to` digest the same way. `pacto publish`
signs each digest it pushes: the contract, each image and the files Flux
applies. Check any of them with cosign and the publisher's public key,
which the team that publishes gives you:

```bash
cosign verify --key cosign.pub registry.example/acme/payments@sha256:...
```

For keyless signing, from CI with no key file, check the identity instead:

```bash
cosign verify --certificate-identity https://github.com/acme/payments/.github/workflows/release.yml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  registry.example/acme/payments@sha256:...
```

If the team publishes with `transparencyLog: false`, add
`--insecure-ignore-tlog` to the key command; the publishing team can tell
you which it uses. `pacto publish` refuses to publish a version it cannot
sign.

To tie a record to a release, take the digest of the subject named `to`
(`jq -r '.subject[] | select(.name=="to") | .digest.sha256'` on the decoded
statement; it prints the bare hex, so put `sha256:` in front of it for
`cosign verify`). It is the deploy artifact, the package of Kubernetes files Flux
applies. It lives in the repository the service's contract names for that
artifact, for example `registry.example/acme/deploy/payments`. Ask the
publishing team for that address, as for the anchor. Check it there with the
`cosign verify` command above. Get the publisher's key out of band too, never
from the registry. See
[Publish a version](publishing.md#signing) for the signing settings.

## Known limits

- Someone who controls both the registry and the cluster can delete the
  newest records and report an older newest id. Compare with the id you kept
  from your last audit.
- With no public log, nothing outside the record proves when it was signed.
  Someone holding the recorder's key could write a record with any date.
- A change running while its Stage is deleted is never recorded.
- Two different names on a record do not always mean two people. When
  `actor.class` is `agent` and `actor.onBehalfOf` is empty, the person behind
  the agent may be the one who approved. Even when it is set, it is only the
  agent's claim.
- Registry clean-up rules can delete records. The platform team must keep
  the streams repository out of them.
- A record made while the cluster's admission checks were off has no
  `guarded: true`.
- A runner's signature proves which runner key signed the answer, not that
  the runner ran every case honestly, and not which model really answered.
  A runner that reports `observedModel` vouches for it with its own key.
- A `mira` or `http` verdict is unsigned, and the record signs only what the
  promoter read. Mira counts telemetry the service reports about itself (its
  own server spans, keyed on the `service.name` and `service.version` it
  sends), so code under test, or any pod that can reach the OTLP endpoint,
  can hide its errors or flood clean spans to force a Pass, or a Fail and
  with it a rollback. See
  [what a health verdict can prove](integrations/kubernetes/delivery-health.md#what-a-verdict-can-prove).
