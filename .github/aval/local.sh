#!/usr/bin/env bash
# Usage: local.sh SUITE... (make aval-local SUITES="unit gates"). A maintainer's
# signed local run on any machine with Docker (CONTRIBUTING.md "Save PR CI
# time: sign a local run"):
#   1. refuses a worktree with changes, and checks HEAD out into
#      .aval/local/<sha>-run (no untracked or ignored file can reach a
#      fingerprint);
#   2. runs each suite there through run.sh inside local.Dockerfile's
#      linux/amd64 image (the platform and tool versions the contracts
#      declare), with the trust policy pr.yaml, recording results in that
#      clone's .aval/ and pushing nothing; the Go, npm and home caches are
#      per commit (.aval/local/<sha>-cache), so one commit's run cannot
#      poison another's;
#   3. checks HEAD out again into .aval/local/<sha>, a clone the container
#      never saw (code under test can plant git hooks, core.fsmonitor or an
#      index in the clone it ran in, and host git would run them), copies
#      only the result, report and record files over (regular files, no
#      symlinks), and on the host signs keyless (interactive Sigstore login)
#      and pushes each result run.sh's AVAL_PUSH=recorded accepts: passed,
#      not reused, reusable, no security event. aval push recomputes the
#      fingerprint and refuses a result for other inputs.
# Only for commits you authored: the container's output is what you sign.
# Both sides run with ci_env below: every variable a contract fingerprints
# (environment.env) unset, except the values CI's suite jobs and aval-publish
# have (setup-go exports GOTOOLCHAIN=local), so the fingerprint is CI's.
# Only suites that need no Docker, kind or cluster inside the container run.
# ui is left out: under emulation on an arm64 Mac two Vitest cases pass their
# 5 s timeout.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"
supported=" unit examples-go demo-validate integration acceptance-go fleet-graph demo-arc gates release-detect "

# ci_env CMD...: CMD with every contract's environment.env unset, then CI's values.
ci_env() {
  local unset=() n
  for n in $(sed -n 's/^  env: \[\(.*\)\]$/\1/p' aval.*.yaml | tr -s ', ' '\n' | sort -u); do
    unset+=(-u "$n")
  done
  env "${unset[@]}" GOTOOLCHAIN=local "$@"
}

if [ "${1:-}" = --in-container ]; then
  shift
  rc=0
  for suite; do
    echo "==> $suite"
    ci_env bash .github/aval/run.sh "$suite" || { r=$?; [ "$rc" = 5 ] || rc=$r; }
  done
  exit "$rc"
fi

[ $# -gt 0 ] || { echo "usage: make aval-local SUITES=\"suite...\" (supported:$supported)" >&2; exit 2; }
[ "${GITHUB_ACTIONS:-}" != true ] || { echo "aval-local: local only; CI signs in aval-publish" >&2; exit 2; }
for suite; do
  [[ "$supported" == *" $suite "* ]] || { echo "aval-local: $suite is not supported: it needs Docker, kind or a cluster inside the container, or times out under emulation; CI runs it (supported:$supported)" >&2; exit 2; }
done
if [ -n "$(git status --porcelain)" ]; then
  echo "aval-local: refusing a worktree with changes; commit or stash them:" >&2
  git status --short >&2
  exit 2
fi
registry=$(sed -n 's/^ *AVAL_REGISTRY: *//p' .github/workflows/ci.yml | sort -u)
[[ "$registry" =~ ^[a-z0-9][a-z0-9./:_-]*$ ]] || { echo "aval-local: ci.yml must declare exactly one AVAL_REGISTRY, got: $registry" >&2; exit 2; }
version=$(sed -n 's/^AVAL_VERSION=//p' .github/aval/install.sh)
aval=${AVAL:-$root/.aval/aval-$version/aval}
[ -x "$aval" ] || bash .github/aval/install.sh

sha=$(git rev-parse HEAD)
# clone DIR: a fresh checkout of $sha in DIR, from the committed tree only.
clone() {
  rm -rf "$1"
  git init -q "$1"
  git -C "$1" fetch -q --depth=1 "$root" "$sha"
  git -C "$1" checkout -q --detach "$sha"
}
run="$root/.aval/local/$sha-run"
clone "$run"

image="pacto-aval-local:$(git hash-object .github/aval/local.Dockerfile | cut -c1-12)"
docker image inspect "$image" >/dev/null 2>&1 \
  || docker build --platform linux/amd64 -t "$image" - < .github/aval/local.Dockerfile
# ponytail: per-commit caches, so every new commit downloads its modules again;
# share GOMODCACHE behind `go mod verify` if that gets slow.
cache="$root/.aval/local/$sha-cache"
mkdir -p "$cache/home"
rc=0
docker run --rm --platform linux/amd64 --user "$(id -u):$(id -g)" \
  -v "$run:/src" -w /src -v "$cache:/cache" \
  -e HOME=/cache/home -e GOCACHE=/cache/go-build -e GOMODCACHE=/cache/gomod -e npm_config_cache=/cache/npm \
  -e CI=true -e AVAL="/src/.aval/aval-$version/aval" -e AVAL_REGISTRY="$registry" -e AVAL_POLICY=/src/.github/aval/pr.yaml \
  "$image" bash .github/aval/local.sh --in-container "$@" || rc=$?
if [ "$rc" = 5 ]; then
  echo "aval-local: SECURITY: a verification failure (see $run/.aval/*-report.json); nothing pushed" >&2
  exit 5
fi

# Never run host git or a host script in $run: the container could write it.
work="$root/.aval/local/$sha"
clone "$work"
mkdir -p "$work/.aval"
[ ! -L "$run/.aval" ] || { echo "aval-local: $run/.aval is a symlink; nothing pushed" >&2; exit 1; }
for suite; do
  for k in result report record; do
    f="$run/.aval/$suite-$k.json"
    if [ -f "$f" ] && [ ! -L "$f" ]; then cp -- "$f" "$work/.aval/"; fi
  done
done
[ "$(git -C "$work" rev-parse HEAD)" = "$sha" ] || { echo "aval-local: $work is not at $sha; nothing pushed" >&2; exit 1; }
failed=()
for suite; do
  echo "==> push $suite"
  ci_env env AVAL="$aval" AVAL_REGISTRY="$registry" AVAL_PUSH=recorded bash "$work/.github/aval/run.sh" "$suite" || failed+=("$suite")
done
echo "aval-local: results, reports and run records in $work/.aval/"
[ ${#failed[@]} -eq 0 ] || { echo "aval-local: not pushed: ${failed[*]}" >&2; exit 1; }
exit "$rc"
