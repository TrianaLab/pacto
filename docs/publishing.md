# Publish a version

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
- `publish.sign` is missing, or the signing key file would end up inside the bundle;
- there is no source revision (run it in a git checkout, or pass `--revision`);
- the version is too small for the change.

The version rule compares the contract with the highest version already
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
| `adopt` | An earlier publish of this version from this revision pushed it | nothing; reuses it |
| `conflict` | The tag holds something else | refuses; nothing is overwritten |
| `unreadable` | The registry could not be read | refuses; unreadable is never absent |

Publishing again finishes what is missing and changes nothing else, so a run
that stopped halfway is simply repeated. The deploy artifact is tagged last:
once its tag exists, everything it points at exists and is signed.

## What gets published

Each build gets `PACTO_OUT`, `PACTO_VERSION`, `PACTO_REVISION` and, for every
artifact built before it, `PACTO_DIGEST_<NAME>` and `PACTO_REF_<NAME>`, so a
manifests build can pin the image it deploys by digest. Pacto then:

1. pushes each artifact by digest, with `org.opencontainers.image.version` and
   `org.opencontainers.image.revision` on its manifest;
2. packages the deploy artifact as a Flux artifact whose `pacto.dev/contract`
   annotation names the digest-pinned contract bundle;
3. attaches an SBOM made by `syft` to each image that asks for one;
4. signs every digest with `cosign`, unless a valid signature by the same key
   is already there;
5. pushes the bundle and writes the tags.

It needs `docker buildx` with a builder that can write an OCI layout (the
`docker-container` driver can) for `dockerfile` builds, `cosign` to sign and
`syft` for SBOMs. Registry credentials and plain-HTTP registries come from the
same settings as `pacto push`.

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
the contract annotation.
