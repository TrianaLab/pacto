# Publish a version

Reference for publishing. To take a service from nothing to its first release,
follow [Pactify a service](pactify.md).

`pacto publish` publishes what a contract's [`publish`](contract-reference/publish.md)
section declares: the contract bundle, the service's images, charts and the
manifests Flux applies, all tagged with the version the contract states. Docker
or a command you name does the build. Pacto pushes, signs and checks.

```bash
pacto publish --plan    # what is missing, and what would refuse it; writes nothing
pacto publish           # publish it
```

## What it checks

Pacto runs no tests. It refuses to publish when one of its own checks fails:

- the contract is invalid, or uses a local reference or breaks its `pacto.lock`;
- `publish.sign` is missing, the signing key file would end up inside the
  bundle, or the signer does not open: a missing key file, a wrong
  `COSIGN_PASSWORD` or an unreachable KMS key refuses the plan, before
  anything is built;
- there is no source revision (run it in a git checkout, or pass `--revision`).
  Pacto reads the commit from `.git` itself, worktrees and packed refs
  included;
- the git checkout has uncommitted changes to tracked files, so the commit
  stamped and signed on every artifact would not hold what was built. Publish
  from a clean checkout. This check runs `git status`; `--revision` skips it,
  since you then name the revision yourself. Untracked files are not checked;
- a `dockerfile` build has no usable `docker buildx` builder, or a `command`
  build's program is not an executable here;
- two artifacts, or an artifact and the bundle, share one repository;
- an SBOM is declared on a `dockerfile` build, which writes none, or sits in
  the contract directory without a `.pactoignore` line (`--plan` names it);
- the version is too small for the change.

A version too small for the change is also kept as a signed refusal in the
contract bundle repository, signed with the publish key, so the dashboard's
**Published** view shows it with the bump it needed. Keeping it never changes
the outcome: if the registry refuses it, publish says so beside the refusal.

A release candidate's fixes ship as the version it was a candidate for: after
`2.0.0-rc.1`, `2.0.0-rc.2` and `2.0.0` are accepted whatever changed. The change
since the last final release must still fit the version: after `1.0.0` and
`1.1.0-rc.1`, a breaking change cannot ship as `1.1.0`. The version
rule compares the contract with the highest version already
published below it, using the same classification as [`pacto diff`](contract-reference/diff.md):

| Change since the last version | Smallest version allowed |
|---|---|
| Breaking | a new major version (before 1.0, a new minor) |
| Potentially breaking: Pacto cannot tell | a new minor version |
| Not breaking | any higher version |

`pacto diff` prints the same rule as `Required bump`.

## Never overwritten

For every artifact the plan says what the registry holds for its tag:

| State | Meaning | Publish does |
|---|---|---|
| `absent` | The tag does not exist | builds, pushes and tags it |
| `identical` | The tag holds exactly what Pacto computed (the bundle) | nothing |
| `adopt` | Its version and revision annotations match this publish | reuses it if it is signed by the configured identity; refuses otherwise |
| `conflict` | The tag holds something else | refuses; nothing is overwritten |
| `unreadable` | The registry could not be read | refuses; unreadable is never absent |

Annotations are not signed, so anyone who can push can write them. Pacto
therefore never signs what it adopts: a tag it wrote was signed before it was
tagged, and one without that signature is someone else's.

A run that stopped halfway finishes when you run it again from the same
commit: the plan adopts what the first run pushed and writes only what is
missing. Run it from another commit and every tag the first run wrote is a
conflict, so that version is spent: publish a higher `service.version`.

The deploy artifact is tagged last: once its tag exists, everything it points
at exists and is signed. The other tags, the bundle's included, may already
exist when a run stops, but no Stage takes a version without its deploy
artifact. Each tag is read again just before it is written, which narrows the
window in which two runs racing on one version could both write. It does not
close it: a registry has no compare-and-swap on tags, so give each version one
publisher.

## What gets published

Each build gets `PACTO_OUT`, `PACTO_VERSION`, `PACTO_REVISION` and, for every
artifact built before it, `PACTO_DIGEST_<NAME>` and `PACTO_REF_<NAME>`, so a
manifests build can pin the image it deploys by digest. Pacto then:

1. pushes each artifact by digest, with `org.opencontainers.image.version` and
   `org.opencontainers.image.revision` on its manifest;
2. packages the deploy artifact as a Flux artifact whose `pacto.dev/contract`
   annotation names the digest-pinned contract bundle;
3. attaches the SBOM file the build wrote to each image that declares one, and
   signs it, unless one signed by the configured identity is already there.
   Pacto never makes an SBOM, and refuses a file older than the build;
4. signs every digest, unless a valid signature by the configured
   key, KMS key or keyless identity is already there, then verifies the new
   signature against that identity. A keyless signature carries whatever
   identity the CI token had, so a mismatch fails here, before any tag is
   written, instead of publishing a version no Stage would take;
5. pushes the bundle and writes the tags.

It needs `docker buildx` with a builder that can write an OCI layout (the
`docker-container` driver can) for `dockerfile` builds, and `git` to check that
the checkout is clean unless you pass `--revision`. It needs no `syft` or
`cosign`. The build commands `pacto init` writes run `sh`, `cp` and `sed`, so
they need a POSIX shell: on Windows, publish from WSL or a Linux CI runner.
Registry credentials and plain-HTTP registries come from the same settings as
`pacto push`.

## Signing

Pacto signs with the Sigstore library and needs no `cosign` binary. Each
signature is a Sigstore bundle attached to the digest as a referrer, the way
cosign 3 writes one, so `cosign verify` and Flux's `spec.verify` both check it.

| `sign` | What signs | Notes |
|---|---|---|
| `key: cosign.key` | A PEM private key file | A cosign key is decrypted with `COSIGN_PASSWORD` |
| `kms: awskms:///alias/ci` | A KMS key | `awskms://`, `gcpkms://`, `azurekms://` or `hashivault://`, with that cloud's usual credentials. The key must be ECDSA P-256, Ed25519 or RSA PKCS#1 v1.5 with SHA-256: Pacto refuses RSA-PSS and SHA-384 or SHA-512 keys. The released `pacto` binaries leave cloud KMS out to stay small: build one with `go install -tags kms github.com/trianalab/pacto/v4/cmd/pacto@<version>`, or one provider's tag (`kms_aws`, `kms_gcp`, `kms_azure`, `kms_hashivault`). The error names the build to use |
| `keyless: true` | A short-lived certificate from public-good Fulcio | The OIDC token comes from `SIGSTORE_ID_TOKEN` or GitHub Actions (`id-token: write`) |

A signature goes to the public transparency log unless `transparencyLog:
false`. Keyless signing needs the log: its certificate lives ten minutes, so
it verifies only at the time the log saw it, and Pacto refuses keyless with
`transparencyLog: false`. Pacto verifies a keyless signature against the
`identity` and `issuer` you configure, under the trusted root Sigstore
publishes through TUF.

## In CI and from agents

CI calls it as one step after the tests pass. An agent calls the same thing
through MCP: `pacto mcp --publish` adds `pacto_publish_plan` and
`pacto_publish` beside the authoring tools, over stdio only.

## Hand-off to delivery

With [Pacto delivery](integrations/kubernetes/delivery.md) installed, a Stage
whose candidate `OCIRepository` follows the deploy artifact's repository sees
each new version without an extra step, and `--propose <namespace>/<stage>`
proposes it on a Stage that does not promote by itself. A Stage promotes only
versions published this way: Flux checks the signature and the promoter checks
the contract annotation. So `--propose` first waits until the Stage records the
version as published, which happens once Flux polls the new tag and verifies
its signature. It waits up to `--propose-timeout` (10 minutes by default), then
gives up and says so.
