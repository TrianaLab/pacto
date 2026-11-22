# Publish from CI with pacto ci --publish

On a merged commit, `pacto ci --publish` builds, signs and publishes every
changed service of a repository, providers before the services that depend on
them. It runs the engine `pacto publish` runs, one service at a time, so a
single-folder `pacto publish` behaves like one row of it.

```bash
pacto ci                    # the plan: what changed and the version each would publish
pacto ci --publish          # on the merged commit: publish every changed service
```

A contract needs neither `service.version` nor a `publish` section. When it
declares them, Pacto uses them as written; when it leaves them out, Pacto
derives them from the folder and the registry and records each derived value
in the [inferred record](contract-reference/inferred-record.md) with its
source. Pacto never writes them into `pacto.yaml`.

## What Pacto derives

| Value | Derived from | Recorded as |
|---|---|---|
| `service.version` | The newest verified version in the bundle repository and the change since it | field `service.version`, source `registry:<repo>@sha256:<digest>` |
| How the image is built | The one build tool the folder shows | field `publish.build`, source `<file>:<line>` |
| What deploys the service | The folder's Kustomization, Helm chart or manifests | field `publish.deploy`, source `<file>:<line>` |
| An image built elsewhere | `--image` or a workload image pinned by digest | field `publish.build`, source `flag:--image` or `<manifest>:<line>` |
| The repositories | The registry `pacto ci` resolved, and the service name | bundle `<registry>/<name>` (or `<registry>/contracts/<name>` when the image already lives at `<registry>/<name>`), image `<registry>/images/<name>` unless the workload's image is under the registry, deploy `<registry>/deploy/<name>` |

A service that depends on one with a derived section sets the dependency's
`ref` to that bundle repository, such as `oci://ghcr.io/acme/contracts/ledger`
when the ledger image lives at `ghcr.io/acme/ledger`.

## The derived version

The baseline is the highest final release in the bundle repository whose
signature verifies against the publisher identity, or that is a counted
registry declaration: this service's declaration, at its own tag, following
the history of the versions below it, signed by the person who asked for it
and allowed by the installation signer policy. Pacto compares the folder with
it, using the same classification as [`pacto diff`](contract-reference/diff.md):

| The bundle repository holds | Pacto publishes |
|---|---|
| No version | `1.0.0` |
| A baseline, and the folder has not changed | nothing: the row reads `unchanged` |
| A baseline and a `NON_BREAKING` change | the next patch, such as `1.4.2` to `1.4.3` |
| A baseline and a `POTENTIAL_BREAKING` change | the next minor, such as `1.4.2` to `1.5.0` |
| A baseline and a `BREAKING` change | the next major (before 1.0, the next minor) |
| A higher tag that is neither | the version above it, with the note `ignored <version>: <reason>`, such as `no signature by the publisher identity` or `declaration signed by bob@acme.dev, not the asker alice@acme.dev` |
| Tags and none counts | nothing: the service fails with `no verifiable baseline in <repo>: no final release there is signed by the publisher identity or is a counted registry declaration; declare service.version to publish` |
| No version, while the other bundle repository of the layout holds some | nothing: the service fails with `bundle repository moved: <repo> holds versions` |
| A release candidate above the baseline that the derived version does not satisfy | nothing: declare `service.version` to publish a candidate line |

A declared `service.version` publishes as written, and one too small for the
change still fails, as with `pacto publish`. Its row reads `unchanged` only
when the bundle at that version is signed by the publisher identity and its
deploy artifact is there.

## Fields carried from the registry

When the baseline is a registry declaration, or cites one in
`inputs.declaration`, `pacto ci --publish` carries the owner and the ranges it
declared into the published `pacto.yaml`, as
[Fields declared in the registry](publishing.md#fields-declared-in-the-registry)
describes. The plan prints one line per field after the row's notes, and the
JSON output lists the same records under `carried`:

```text
invoices: owner: billing, declared in registry (rev sha256:7c1d2e3f4a5b)
invoices: dependencies.ledger.compatibility: ^2.3.0, accepted in registry (rev sha256:7c1d2e3f4a5b)
invoices: service.version: 0.2.0, derived from 0.1.0 (rev sha256:7c1d2e3f4a5b)
```

| Line | Means |
|---|---|
| `<field>: <value>, declared in registry (rev ...)` or `accepted in registry (rev ...)` | carried into the published `pacto.yaml` |
| `<field>: <value>, from pacto.yaml (registry declares <value>, rev ...)` | the repository writes the key, so its value wins |
| `<field>: <value>, declared in registry (rev ...), not carried: <reason>` | dropped, such as an owner the owner rule refuses |
| `service.version: <v>, from pacto.yaml` or `derived from <baseline> (rev ...)` | where the version comes from |

The plan reads its baseline unverified and says `baseline unverified in plan
mode`. It carries only from a declaration that counts, and from the newest
final release, never a candidate. The publish then checks that baseline: when
it is neither signed by the publisher identity nor a counted declaration, the
service fails instead of publishing what the plan carried from it. When the
publish measures the version from another release that declares fields the
plan did not carry, the service fails too, so a declared field never drops
without a line. A declaration that could not be checked reads `unverified` in
the plan and fails that service in the publish.

## How the image is built

Pacto runs each program directly, never through a shell, in the service folder.

| Tool | Found by | What Pacto runs |
|---|---|---|
| Skaffold | `skaffold.yaml` with one artifact | `skaffold build --push=false`, then `docker image save` |
| ko | `.ko.yaml` | `ko build --push=false --bare --sbom=none --oci-layout-path=...` |
| Jib | `pom.xml` or `build.gradle[.kts]` naming the Jib plugin | `./mvnw` or `mvn` `jib:buildTar`; `./gradlew` or `gradle` `jibBuildTar` |
| buildpacks | `project.toml` | `pack build`, then `docker image save` |
| Dockerfile | one `Dockerfile` | `docker buildx build` into an OCI layout |

Skaffold wins when the folder holds it, because it drives the others. Any
other pair is a finding that asks for `--image` or a `publish` section, such as
`finding: services/api: both ko (.ko.yaml) and Dockerfile found; pass --image or declare publish`.
A folder with no build tool needs `--image` too. `pacto publish --plan` checks
that each program and `docker` (for Skaffold and buildpacks) is installed
before anything is built.

`--image <service>=<repo>@sha256:<digest>`, or a workload image already pinned
by digest, skips the build. The deploy artifact pins that digest, and Pacto
writes, signs and copies nothing in the image's repository.

## What the deploy artifact holds

Pacto assembles the deploy artifact itself from one deploy source per folder;
two sources are a finding that asks for a `publish` section. It reads the
folder through the root `.pactoignore` and pins the image by digest.

| Source | What ships |
|---|---|
| Plain manifests | Each file under its folder path, each `image:` of the built repository set to `<repo>@<digest>` |
| Kustomization | The kustomization no other one names, and only the files its `resources`, `patches`, `components` and `configMapGenerator` name, at their folder paths, each kustomization with an `images:` entry for the digest. A kustomization in a subfolder, such as `k8s/`, keeps that path, so the applier reads the artifact from it |
| Helm chart | The chart packaged as a `.tgz`, filtered by `.helmignore`, with `version` and `appVersion` set to the service version and the image digest in `values.yaml`. The chart is the deploy artifact |
| `delivery/pacto.yaml` | Ships with the version filled in: as `pacto-resource.yaml` beside the manifests, also added to the root kustomization's `resources`, or as the chart's `templates/pacto-resource.yaml` |

Pacto leaves out, with a plan note:

- every file holding a `kind: Secret`;
- a kustomization with a `secretGenerator`, and the files and env files it names;
- a chart's per-environment `values-<env>.yaml` files;
- files listed in `.pactoignore`.

A symbolic link, or a Kustomization path outside the folder, fails the
service. Every file Pacto would ship is scanned for sensitive literal values,
such as a password in a ConfigMap or a chart's values: a hit fails that service
and names the file. A values key that only names a Secret, such as
`existingSecret: my-secret`, can be a hit too; move it to a `values-<env>.yaml`
file, which never ships.

## Signing

| The contract | Pacto signs with |
|---|---|
| Declares no `publish.sign`, or no `publish` section | keyless, as the identity the CI's OIDC token names |
| Declares `sign.key` or `sign.kms` | that key |
| Declares `sign.keyless` with `identity` and `issuer` | keyless, verified against that identity |
| Declares no `publish` section, with `--sign-key <file>` | that key; add `--no-transparency-log` to keep its signatures out of the public log |

The identity read from the token:

| CI | Token | Identity |
|---|---|---|
| GitHub Actions | `permissions: id-token: write` | `https://github.com/` + `job_workflow_ref`. When that names a reusable workflow of another repository, such as pacto-actions, an earlier version verifies when that workflow at any commit, or a workflow of your repository, signed it on the repository and branch the token names |
| GitLab CI | an `id_tokens` entry named `SIGSTORE_ID_TOKEN` with `aud: sigstore` | `https://` + `ci_config_ref_uri` |
| Other | `SIGSTORE_ID_TOKEN` | the token's `email` claim, else declare `publish.sign.identity` |

The row prints the identity it signed as. Pacto verifies each new signature
against it before any tag is written. `--sign-key`, `--image` and `--registry`
apply only to a contract that declares no `publish` section, and
`--no-transparency-log` needs `--sign-key`.

## What it refuses

- A pull or merge request pipeline: `refused: pull request pipelines do not publish; publish runs on the merged commit`,
  before any token is asked for or anything is built.
- A stale checkout: `superseded: <service> <version> was published from <revision>, which this checkout does not contain`.
  In a shallow clone without that commit, Pacto compares the commit times; a
  baseline published before this check says `fetch more history to check` and
  the publish goes on.
- A contract behind the current `pactoVersion` publishes as the current
  version when that keeps its meaning, else as written, with a plan line (see
  [Older contracts](ci.md#older-contracts)). One kept behind that declares no
  `publish` section or no version fails that service and asks to set the
  current `pactoVersion`.

## Tags, races and order

- Pacto reads each tag before writing it and reads it back after, and reads
  every tag of the service once more after the deploy tag. A tag another run
  wrote in between fails that service.
- A run that loses a derived version to a concurrent run plans again, up to 3
  times: `payments 1.5.0 published by another run; re-planned` and
  `(attempt 2/3)`. A declared version, or a tag signed by another identity, is
  never planned again.
- A registry has no conditional tag write, so two runs writing in the same
  instant after the final read can still overwrite. Close that window with tag
  immutability (ECR, GAR, ACR or Harbor) or a CI `concurrency:` group that runs
  one publish per repository at a time.
- A service that fails blocks the services of the run that depend on it.
  So does a `BREAKING` change whose new version a consumer's range does not
  accept; a `compatibility: unknown` range never counts as accepting. A
  dependency cycle publishes in name order.

## Reproducible builds

Two runs over the same commit publish byte-identical bundles, charts and
manifests. Image builders get `SOURCE_DATE_EPOCH`: the environment value, else
the commit time of `HEAD`. Without either, the row says
`not reproducible: no commit time (set SOURCE_DATE_EPOCH)`. Skaffold and
buildpacks build through the Docker daemon, so their images are as
reproducible as the tool makes them.

## Output, exit codes and records

| Output | Content |
|---|---|
| Table | One row per service: `published`, `unchanged`, `failed`, `blocked` or `refused`, the version and the signing identity |
| `--output-format json` | The plan with each row's `state`, `identity` and `images` |
| Job summary | `$GITHUB_STEP_SUMMARY` on GitHub, `pacto-plan.md` on GitLab |
| Images record | A signed referrer of each bundle listing every image the deploy pins, with its digest and whether Pacto built it |
| Run record | One per published, failed, blocked or refused service; a blocked service is a refusal with reason `dependency` |

The exit status is `0` when every changed service is published or already
published, else `1`. A run that finds no `pacto.yaml`, or skips a folder it
cannot read, exits `1`. To publish one folder by hand, `pacto publish` takes the
same `--image`, `--registry`, `--sign-key` and `--no-transparency-log` flags;
see [Publish a version](publishing.md).

## Publish a breaking change

A breaking change needs a new major version (`1.4.2` becomes `2.0.0`):
`pacto publish` refuses a breaking change with a smaller version. Do not
remove fields to get past the check. Raising `pactoVersion` never hides a
breaking change: `pacto ci` publishes an older contract as the current version
only when that keeps its meaning. Announce the break in `breaks` and each
consumer owner accepts it with `pacto accept`; the gate clears once every
consumer accepted. See [Ship a breaking change](ci.md#ship-a-breaking-change).
