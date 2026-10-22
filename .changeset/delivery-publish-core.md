---
"@pacto/core": minor
---

Add `pacto publish`, which publishes what a contract declares as the version
it states, and contract format 2.1, which declares it.

`pactoVersion: "2.1"` adds one section, `publish`: the repository of the
contract bundle, the artifacts the service publishes (an image, a chart or
manifests) with where each goes and how it is built (`docker buildx` into an
OCI layout, or a command that writes to `$PACTO_OUT`), the deploy artifact Flux
applies and how everything is signed. Keyless signing names the certificate
`identity` and `issuer` it produces, so Pacto can tell its own signature from
anyone else's. A 2.0 contract keeps working unchanged,
and `publish` in a 2.0 contract fails validation. Pacto versions older than
this one reject a 2.1 contract.

`pacto publish --plan` shows, for every artifact, whether its tag is absent,
identical, adopt, conflict or unreadable and which of Pacto's checks refuse
the publish, and writes nothing. `pacto publish` builds what is absent, pushes
it by digest with its version and source revision on the manifest, attaches
SBOMs made by `syft`, signs every digest and verifies each new signature
against the configured identity (a keyless signature by the wrong CI identity
fails before any tag is written), pushes the bundle and
writes the tags with the deploy artifact last; the deploy artifact names its
digest-pinned contract in `pacto.dev/contract`. It never overwrites a tag: each tag is read again
just before it is written. A second run changes nothing and a run that stopped
halfway finishes when run again. A tag whose provenance annotations match is
reused only when it carries a signature by the configured identity, and an SBOM
only when it does too: Pacto never signs content it did not publish. After a
release candidate, its fixes ship as the version it was a candidate for, but
the change since the last final release must still fit the version. It refuses an invalid contract, local references, a broken lock, a
missing signing configuration, a signing key inside the bundle, a missing
source revision and a version too small for the change: a breaking change
needs a major version (a minor before 1.0) and a change Pacto cannot classify
at least a minor. `--propose <namespace>/<stage>` hands the deploy artifact to
Pacto delivery once the Stage recorded it as published, waiting up to
`--propose-timeout` for Flux to verify it. `pacto mcp --publish` adds `pacto_publish_plan` and
`pacto_publish` for agents, over stdio only.

Pacto signs with sigstore-go and needs no `cosign` binary. A signature is a
Sigstore bundle referrer, as cosign 3 writes it, so `cosign verify` and Flux's
`spec.verify` check it. A key file (a cosign key, decrypted with
`COSIGN_PASSWORD`, or a plain PEM key) and a KMS key (`awskms://`, `gcpkms://`,
`azurekms://`, `hashivault://`) sign with or without the transparency log. A
KMS key must be ECDSA P-256, Ed25519 or RSA PKCS#1 v1.5 with SHA-256: Pacto
loads the key once per run, signs a probe then and refuses RSA-PSS and SHA-384 or SHA-512
keys. Only a referrer whose signed statement is a cosign signature counts as
one, and an unreadable referrer does not hide the others. One check reads at
most 256 referrers and 32 signatures and says how many it left unread.
Keyless signing uses public-good Fulcio and Rekor with the OIDC token in
`SIGSTORE_ID_TOKEN` or from GitHub Actions, verifies under the trusted root
Sigstore publishes through TUF and needs the transparency log: a short-lived
certificate verifies only at the time the log saw it, so `transparencyLog:
false` with keyless is refused.

`pacto diff` reports `requiredBump`, the same rule. `pacto push` succeeds
without writing when the registry already holds the same bundle, and now
refuses different content under an existing reference instead of only warning
(`--force` still overwrites).

Pacto delivery promotes only versions `pacto publish` published: the
`pacto.published` pre-gate admits a promote only for a digest Flux verified the
signature of on the Stage's candidate source, whose artifact names its
contract, or a digest the upstream Stage offers: one that passed this gate
there, not on an accepted unknown, and verified. Anything else,
including a candidate from an upstream Stage that does not exist or cannot be
read, is refused as `NotPublished` before any other gate runs.
