# GitHub Actions Integration

[pacto-actions](https://github.com/TrianaLab/pacto-actions) runs Pacto in
GitHub Actions. Its reusable workflow plans every service of a repository on
pull requests and publishes the changed ones on merges to the default branch,
with `pacto ci` as its only step. Its composite action runs single Pacto
commands in jobs you write.

---

## Quick start

`pacto ci init` writes this caller to `.github/workflows/pacto.yml`:

```yaml
--8<-- "examples/ci/github/pacto.yml"
```

The `uses:` line pins a pacto-actions commit, with the release in the
comment, the form Dependabot rewrites when it bumps the pin. See
[Put it in your repository](ci.md) for what the plan shows.

## Jobs and permissions

| Job | Runs when | Runs | Permissions |
| --- | --- | --- | --- |
| `plan` | a pull request | `pacto ci`, then the job summary and one annotation per changed service | `contents: read`, `packages: read` |
| `publish` | a push to the default branch, with `publish` on | `pacto ci --publish`, one run per repository at a time | `contents: read`, `packages: write`, `id-token: write` |
| `comment` | a pull request from the same repository, with `comment` on | posts or updates one plan comment | `pull-requests: write` |

GitHub checks every job's permissions against the caller's before it starts
the workflow, skipped jobs included, so a caller grants the whole map in the
quick start. Each job then takes only its own. A push to any other branch runs
no job.

Only `publish` holds `id-token: write`. GitHub mints the OIDC token for that
job, and the Pacto CLI in it exchanges the token with Sigstore for a
short-lived certificate; the token never leaves the job, and the Pacto
installation never holds it.

## Inputs

| Input | Default | Meaning |
| --- | --- | --- |
| `path` | `.` | root `pacto ci` walks for `pacto.yaml` files |
| `registry` | empty | registry the services publish to, such as `ghcr.io/acme`; empty resolves it from the platform |
| `pacto-version` | empty | Pacto release to run; empty runs the release this pacto-actions release pins and checks by sha256; `latest` runs the newest release unpinned |
| `pacto-artifact` | empty | name of an artifact of the same run holding a static linux `pacto`, used instead of the download |
| `publish` | `true` | publish on a push to the default branch |
| `comment` | `false` | post the plan as one sticky comment on pull requests from the same repository |
| `fail-on` | `blocked` | `blocked` fails the plan job on exit `1`; `unverified` also fails it on exit `2` |
| `transparency-log` | `true` | with `sign-key`, record the signatures in the transparency log |

## Secrets and outputs

| Secret | Use |
| --- | --- |
| `registry-username`, `registry-password` | registry login for a registry other than GHCR; GHCR uses the workflow's token |
| `sign-key`, `sign-key-password` | a cosign key `publish` signs with instead of keyless; written to the runner's temporary directory only |

Keyless signing records the repository, workflow and commit in the public
Rekor log. A private repository that must not appear there sets the
`sign-key` secret, or declares `publish.sign` with a private log.

The workflow outputs `gate` (`clear`, `blocked` or `unverified`) and
`exit-code` (`0`, `1` or `2`) from the plan job.

## Annotations

The plan job annotates each changed service on its `pacto.yaml`: an error when
it is blocked, a warning when it is unverified and a notice when it is clear.
GitHub shows at most 10 annotations of each level per step; the job summary
lists every service.

## Commands for your own jobs

The composite action `TrianaLab/pacto-actions@<sha>` takes a `command`:

| Command | Runs |
| --- | --- |
| `setup` | installs `pacto`, pinned by version and sha256 |
| `ci` | `pacto ci`, with `gate` and `exit-code` outputs and the `fail-on` input |
| `publish` | `pacto ci --publish`; the job needs `id-token: write` for keyless signing |
| `impact`, `validate`, `diff`, `doc` | the Pacto command of that name |

A service that builds with ko, Jib or buildpacks runs `command: setup`, its
build and `command: publish` in a job of its own.

## Moving from v1

`TrianaLab/pacto-actions@v1` keeps working and stays at v1. v2 removes the
`push` command: replace it, and the jobs that ran `validate`, `diff` and
`pacto publish`, with the caller above;
[From an imperative pipeline to one step](ci-from-pipeline.md) maps each one.
