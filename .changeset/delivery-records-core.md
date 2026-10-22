---
"@pacto/core": minor
---

Add the signed record format for Pacto delivery and share one registry write
path between evidence and records.

`pkg/delivery` defines the record of an operation: an in-toto Statement v1
with the predicate type `pacto.dev/operation/v1`. It says what was asked, who
asked and on whose behalf, the Stage policy, every verdict with its proof,
every approval and the outcome, and it places the record in its Stage's stream
by `seq` and `parent`. An operation that fails a check before signing (a
missing field, an approval no longer bound to the operation or a value that
looks like a secret) becomes an `unrecordable` record that names the check and
the intent digest and leaves the rest out, and a seq no operation holds becomes
a `gap` record. `SignRecord` signs a statement as a DSSE envelope in a Sigstore
bundle v0.3 and returns its id, the sha256 of the payload; `OpenRecord`
verifies one. The new `pkg/signing` signs and verifies with sigstore-go: with
an Ed25519, ECDSA P-256 or RSA key, a KMS key or keyless with a Fulcio
certificate. Every record verifies with standard Sigstore tooling, with no
Pacto involved. Operation status gains `record` and `stageUID`, the Stage that
gave it its number, and the Stage stream `recordedSeq` and `head`.

The new `pkg/ocireferrer` is the one way Pacto writes OCI referrers: it opens a
repository with the native Referrers API required (no tag fallback), builds a
deterministic one-layer referrer manifest, pushes it and confirms the
Referrers API lists it. The Evidence Server stores evidence through it.

Records are verified against a trust config that lists which keys may sign the
records of which streams, so a dev recorder cannot sign prod history and an old
key stays trusted while you rotate to a new one. A keyless entry names the
identity, the issuer and the Sigstore trusted root (`trustedRoot`, a
`trusted_root.json`) its certificate must chain to; without one it is refused.
A keyless record verifies only at the time a timestamp authority or the
transparency log saw it signed. Every record cites the trust config the
recorder checked the stream with.

History is read from the records. `pacto delivery history` reads a Stage's
signed records when the recorder runs, plus the operations not recorded yet,
and falls back to the operations in the cluster otherwise. Every answer says
`asOf`, `source` and `completeness`, lists the problems the records show
(missing numbers, forks, broken chains, gaps and changes nobody recorded) and
what could not be read, and a missing record is never read as nothing having
happened. A number the Stage gave out with neither a record nor an Operation
left is a problem too, and an Operation whose record did not verify is marked
recorded but not verified. `pacto delivery blame` says who put a version in
place, on whose behalf, who approved and what every check said, and names the
parts read from an Operation's unsigned status, which make the answer partial. `pacto delivery undo` returns
the plan that would undo an operation and proposes nothing. `pacto delivery
record verify` checks every record and the chain and prints the cosign
command that checks one record without Pacto. A plan now lists the precedent:
what the records attached to the version say happened to it on other Stages.
`pacto delivery history --all` and `record verify --all` walk every Stage you
can list, up to 500 Stages, eight at a time and for at most two minutes, with
one `asOf` and a completeness that is complete only when every Stage was read.
Without the right to list Stages in every namespace they walk each namespace
you can read and list the others as unreadable.
The same answers are the MCP tools `pacto_delivery_blame`,
`pacto_delivery_undo` and `pacto_delivery_record_verify` (`pacto_delivery_history`
and `pacto_delivery_record_verify` walk every Stage when `stage` is empty), and the dashboard's
History view says whether each history comes from signed records or only from
the cluster. Pass the trust config with `--trust`, `--delivery-trust` or
`PACTO_DELIVERY_TRUST`.
