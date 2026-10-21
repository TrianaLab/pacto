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
a `gap` record. `SignRecord` signs a statement as a DSSE envelope in a sigstore
bundle v0.3 and returns its id, the sha256 of the payload; `OpenRecord`
verifies one. Ed25519 signing is built in, and the verifiers accept Ed25519,
ECDSA and RSA keys and keyless certificates by identity and issuer. Operation
status gains `record` and the Stage stream `recordedSeq` and `head`.

The new `pkg/ocireferrer` is the one way Pacto writes OCI referrers: it opens a
repository with the native Referrers API required (no tag fallback), builds a
deterministic one-layer referrer manifest, pushes it and confirms the
Referrers API lists it. The Evidence Server stores evidence through it.
