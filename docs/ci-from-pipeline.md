# From an imperative pipeline to one step

A pipeline that installs Pacto and runs `pacto validate`, `pacto diff`,
`pacto impact` and `pacto publish` as separate steps becomes one `pacto ci`
step: a plan on every pull request and `pacto ci --publish` on every merge to
the default branch. This page maps each old step to what `pacto ci` does and
shows the result for GitHub, GitLab and a monorepo. Every example on it is a
file that Pacto's own CI runs.

## What each step becomes

| Old step | With `pacto ci` |
| --- | --- |
| `curl -fsSL .../get-pacto.sh \| bash` | The reusable workflow and the GitLab component install a pinned Pacto release and check its sha256 |
| `pacto validate .` | Every `pacto.yaml` under the root is validated as part of the plan |
| `pacto diff oci://ghcr.io/acme/contracts/my-service .` | Each service is compared with its last published version; `CLASSIFICATION` shows the result. With the reusable workflow's defaults a `BREAKING` change with consumers reads `unverified` and passes with a warning, where `pacto diff` failed; `fail-on: unverified` keeps the check red |
| `continue-on-error: true` on the diff before the first publish | A service with no published version plans as a first publish |
| `pacto impact ... --root ./services/checkout` | The organization index names the consumers; `--root` is needed only for one that is not published yet. Without the delivery trust config a change with consumers reads `unverified`, a warning under the default `fail-on: blocked` |
| `docker buildx create` plus `pacto publish` | `pacto ci --publish` builds with the tool the folder shows, Docker Buildx included, and publishes every changed service |
| `COSIGN_KEY` written to `cosign.key` | Keyless signing by default; the `sign-key` secret keeps signing with your key |
| Raising `service.version` by hand | The version is derived from the change since the last published one |
| `pacto push` of the contract alone | Declare `publish.bundle` and `pacto ci --publish` publishes the contract |

Keep your key while you move. A first keyless publish over versions signed
with a key finds no baseline it can verify and stops with
`no verifiable baseline in <repo>`. Keep the `sign-key` secret until a version
signed keyless exists, or declare `publish.sign` in `pacto.yaml`. A repository
that already published keyless from its own workflow keeps verifying.

## GitHub

Delete the jobs that installed Pacto, validated, diffed, ran impact and
published, and commit the caller `pacto ci init` writes:

```yaml
--8<-- "examples/ci/github/pacto.yml"
```

To post the plan on the pull request as well, add one line:

```yaml
jobs:
  pacto:
    uses: TrianaLab/pacto-actions/.github/workflows/ci.yml@<sha> # v2.0.0
    with: {comment: true}
```

The publish job builds with Docker on the runner. A service built with ko,
Jib or buildpacks builds in a job of your own and pins its image by digest in
its manifests, which `pacto ci --publish` then uses without a build. See
[GitHub Actions](github-actions.md) for every input.

## GitLab

Delete the Pacto jobs from `.gitlab-ci.yml` and let `pacto ci init` add the
component to the `include` list:

```yaml
--8<-- "examples/ci/gitlab/.gitlab-ci.yml"
```

`pacto-plan` runs in the `test` stage on merge request pipelines and keeps
the plan as the `pacto plan` artifact. `pacto-publish` runs in the `deploy`
stage on the default branch with an ID token for keyless signing; it builds
with a Docker-in-Docker service, so its runner must allow privileged jobs. The
`stage-plan` and `stage-publish` inputs name other stages. A self-managed
instance that blocks `raw.githubusercontent.com` copies `templates/pacto.yml`
from pacto-actions into a project and includes it from there.

## Any other CI

The same command, with the same exit codes, runs anywhere Pacto is installed:

```sh
--8<-- "examples/ci/any-ci.sh"
```

## A monorepo

`pacto ci` walks every folder under the root, so a repository of many
services needs no per-service job. The root `.pactoignore` names the folders
to skip ([how](pactoignore.md#at-the-repository-root)). In the demo monorepo,
`examples/demo/monorepo`, one service has an empty `pacto.yaml` and the other
has two lines naming its owner. Its first plan:

--8<-- "examples/demo/generated/ci-monorepo-plan.md"

Both rows read `unverified` because each name is inferred from the folder,
and an inferred name never clears the gate. Declare `service.name` in each
`pacto.yaml`, pass the registry (the `registry` input, or `--registry`) and
the first publish reads clear.
