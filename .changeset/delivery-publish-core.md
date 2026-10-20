---
"@pacto/core": minor
---

Add `pacto publish`, which publishes what a contract declares as the version
it states, and contract format 2.1, which declares it.

`pactoVersion: "2.1"` adds one section, `publish`: the repository of the
contract bundle, the artifacts the service publishes (an image, a chart or
manifests) with where each goes and how it is built (`docker buildx` into an
OCI layout, or a command that writes to `$PACTO_OUT`), the deploy artifact Flux
applies and how everything is signed. A 2.0 contract keeps working unchanged,
and `publish` in a 2.0 contract fails validation. Pacto versions older than
this one reject a 2.1 contract.

`pacto publish --plan` shows, for every artifact, whether its tag is absent,
identical, adopt, conflict or unreadable and which of Pacto's checks refuse
the publish, and writes nothing. `pacto publish` builds what is absent, pushes
it by digest with its version and source revision on the manifest, attaches
SBOMs made by `syft`, signs every digest with `cosign`, pushes the bundle and
writes the tags with the deploy artifact last; the deploy artifact names its
digest-pinned contract in `pacto.dev/contract`. It never overwrites a tag, a
second run changes nothing and a run that stopped halfway finishes when run
again. It refuses an invalid contract, local references, a broken lock, a
missing signing configuration, a signing key inside the bundle, a missing
source revision and a version too small for the change: a breaking change
needs a major version (a minor before 1.0) and a change Pacto cannot classify
at least a minor. `--propose <namespace>/<stage>` hands the deploy artifact to
Pacto delivery. `pacto mcp --publish` adds `pacto_publish_plan` and
`pacto_publish` for agents, over stdio only.

`pacto diff` reports `requiredBump`, the same rule. `pacto push` succeeds
without writing when the registry already holds the same bundle, and now
refuses different content under an existing reference instead of only warning
(`--force` still overwrites).

Pacto delivery promotes only versions `pacto publish` published: the
`pacto.published` pre-gate admits a promote only for a digest Flux verified the
signature of on the Stage's candidate source, whose artifact names its
contract, or a digest the upstream Stage verified. Anything else is refused as
`NotPublished` before any other gate runs.
