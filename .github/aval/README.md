# How Pacto's CI runs its tests through Aval

Every test suite runs by one path: a workflow step runs a make target, the
target runs `run.sh <suite>`, and `run.sh` runs `aval run -f
aval.<suite>.yaml`. Lint, build, drift and release dry-run jobs do not run
tests and stay outside Aval.

## Files

| File | Role |
|------|------|
| `install.sh` | installs the pinned aval release into `.aval/` after checking its sha256 and the Sigstore signature of `checksums.txt` (`make aval-install`) |
| `run.sh` | the one path from a make target to a suite; writes `.aval/<suite>-{result,report,record}.json` |
| `trust-from-base.sh` | sets `AVAL_POLICY` and `AVAL_REGISTRY` in a CI job, always from `main` |
| `main.yaml` | policy on a push to `main`: only `ci.yml` on `main` or on a pull request, at runner level |
| `pr.yaml` | policy on a pull request: the same identities, minimum level developer |
| `local.sh`, `local.Dockerfile` | `make aval-local`: a maintainer's run in a `linux/amd64` container, signed on the host |
| `summary.sh` | job summary; fails on a verification failure, even beside a verified result |
| `publish.sh` | signs and pushes recorded results (job `aval-publish`) |

## A suite job

1. Installs the toolchain the contracts probe, at the exact versions they
   declare (Go from `go.mod`, Node 22.23.4, Helm v4.3.1, kind from
   `make kind-install`, Compose v5.5.0) and cosign.
2. Runs `main`'s copy of `trust-from-base.sh`: `main.yaml` on a push to
   `main`, `pr.yaml` on a pull request, no policy on the nightly or a manual
   run (every suite runs). The registry is the one `AVAL_REGISTRY` that
   `main`'s `ci.yml` declares.
3. Runs `make aval-install`, then its make target.
4. Always runs `summary.sh` and uploads `.aval/*.json` as `aval-<job>`.

A verified passing result for the same inputs is reused; a miss runs the
suite; a verification failure (exit 5) fails the job and is never retried
into silence. So does a fingerprint flooded with more attestations than
Aval lists (`security.jq` is the one check for both). `external-dependencies` suites (they read something outside
their fingerprint) and `non-deterministic` ones (perf budgets) always run.
Suite jobs hold no registry write and no OIDC token, so a fork pull request
runs them as is: it reads `main` and the public attestations anonymously.

## On a push to main or a pull request

Both jobs run on a push to `main` and on a pull request from a branch of this
repository that is maintainer-authored and maintainer-triggered: author,
actor and triggering actor are `edu-diaz` (the exact `if:` is pinned by
`tests/architecture/aval_test.go`). Anyone can open a pull request from an
existing branch, so where the head lives is not enough. Forks and Dependabot
never match; a fork gets no OIDC token.

- `aval-publish` (`packages: write`, `id-token: write`, runs no suite)
  downloads each job's artifact into its own directory, refuses a result
  from an artifact whose job does not run that suite (the table in
  `publish.sh`), skips any that were reused, failed, are `non-deterministic`
  or `external-dependencies` (no policy reuses them) or sit beside a security
  event, and signs and pushes the rest keyless to
  `ghcr.io/trianalab/pacto-attestations`.
  `aval push` recomputes the fingerprint from the same commit and refuses a
  result recorded for other inputs.
- `aval-record` (`packages: write` only) publishes every run record to
  Aval's public stats registry, `ghcr.io/trianalab/aval-stats`. It is best
  effort and never fails the build.

## What protects main

`main` reuses only results its own CI signed: `ci.yml` on `main`, or `ci.yml`
on a pull request (`refs/pull/<n>/merge`). After a squash merge of an
up-to-date branch the inputs are the same, so `main` does not run again what
the pull request proved. A pull request also reuses a maintainer's signed
local run, at developer level; `main` never does. CI does not sign a suite
again that the pull request reused from such a run, so `main` runs it once
more.

A pull request runs its own `ci.yml`, so whoever can push a branch can make
it sign any statement. Trusting pull request results is sound only while
every account with push access is a maintainer (today only edu-diaz), forks
get no OIDC token, signing needs a maintainer-authored and
maintainer-triggered pull request, and nothing else pushes to a branch here:
`ui-rebuild-commit.yml` pushes only Dependabot's rebuild to Dependabot's own
pull request, at the commit that run built. Before granting push access to
anyone else, drop the pull request identity from `main.yaml` and `pr.yaml`.
Adding `.github/**` to suite scopes would not help: a forging workflow signs
whatever fingerprint it likes.

`tests/architecture/aval_test.go` checks the policies, the job permissions,
the wiring of every suite job, that no workflow runs a test directly, that
every contract declares its platform and fingerprints cleanly, that the
registry is declared once and that the release is pinned.

## One-time setup

- Give `TrianaLab/pacto` write access to the `aval-stats` package ("Manage
  Actions access").
- Make `pacto-attestations` public right after the first push (from
  `main`). Until then every lookup fails, every suite runs, and each `main`
  push adds another attestation per fingerprint (pull requests and
  `make aval-local` do not push after a failed lookup). Past 64 the lookup
  reports flooding (exit 5).
