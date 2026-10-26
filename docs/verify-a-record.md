# Verify a record

This page is for an auditor or security reviewer who must check what Pacto
did, ideally without trusting Pacto. It shows how to read a record, check its
signature with standard tools and check that a released version was signed.
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
| Registry | The container registry where your images live, such as zot or Harbor |
| Digest | The `sha256:...` fingerprint of one exact image or file in a registry |
| Referrer | A file a registry stores "attached" to a digest, found by asking the registry what points at that digest |

## What a record says

A record answers: what was asked, by whom, for which Stage, what each check
said and why, who approved, and how it ended. Refusals are recorded too. It
holds references, never secrets. A record is an
[in-toto](https://in-toto.io) statement, a standard format for signed claims
about software, with the type `delivery.pacto.trianalab.io/operation/v1`.

Two fields matter for trust:

- `guarded: true` means the cluster itself checked who asked and who
  approved. Without it, those names are only what the writer claimed.
- `chain.seq` and `chain.parent`: each Stage's records are numbered 1, 2, 3
  and so on, and each one names the record before it. A missing number or a
  broken link means part of the history is gone.

Records are off unless your platform team turned on the recorder, and they
need a registry that supports referrers (zot does; GitHub's registry does
not).

## Where records live

Each record is stored in your registry as a referrer, attached to the version
the change moved to, the version it moved from, the new contract and one
fixed digest per Stage called the **anchor**. The anchor is where the whole
history of one Stage hangs. Its full address is in the Stage:

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
REC=sha256:...                                         # pick one from the list
REPO=${ANCHOR%@*}
oras blob fetch --output record.json \
  "$REPO@$(oras manifest fetch "$REPO@$REC" | jq -r '.layers[0].digest')"
cosign verify-blob-attestation --bundle record.json --key recorder.pub \
  --type delivery.pacto.trianalab.io/operation/v1 --insecure-ignore-tlog \
  --digest "${ANCHOR##*sha256:}" --digestAlg sha256
jq -r .dsseEnvelope.payload record.json | base64 -d | jq .predicate   # read it
```

cosign prints `Verified OK` when the signature holds and exits non-zero when
it does not. `--insecure-ignore-tlog` is expected: the recorder never writes to a public
log, because records name your services. The signature alone proves the
record was not changed.

## Check the whole history

To check the chain yourself, fetch every record on the anchor and check
that the `seq` numbers run with no hole and each `parent` names the record
before. A **gap** record is a signed placeholder for a number no change used,
so it does not count as a hole. In the record, they are `.predicate.chain.seq` and `.predicate.chain.parent`.
With cluster access, Pacto does the same check in one command and exits
non-zero if anything is wrong:

```bash
pacto delivery record verify -n prod --stage payments --trust recorder.pub
pacto delivery blame -n prod --stage payments --trust recorder.pub   # who asked and approved what runs now
```

When Pacto cannot read every record, its answer says `partial`. Then a
missing change may still have happened: no answer means "unknown", never
"nobody".

## Check a version was signed

Every version Pacto ships was published and signed with `pacto publish`.
That signs each digest it pushes: the contract, each image and the files
Flux applies. Check any of them with cosign and the publisher's public key,
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
`--insecure-ignore-tlog` to the key command. Pacto refuses a publish it
cannot sign, so an unsigned version never reaches a Stage. See
[Publish a version](publishing.md#signing) for the signing settings.

## Known limits

- A change running while its Stage is deleted is never recorded.
- Registry clean-up rules can delete records. The platform team must keep
  the streams repository out of them.
- A record made while the cluster's admission checks were off has no
  `guarded: true`.
