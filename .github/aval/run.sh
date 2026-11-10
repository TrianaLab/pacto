#!/usr/bin/env bash
# Usage: run.sh SUITE. Every test target in Makefile/ci.mk calls this; it is the
# one path from a make target to a suite, locally and in CI.
#
# It runs aval.SUITE.yaml at the repository root through `aval run` when
# AVAL_REGISTRY is set or under GitHub Actions. A verified passing result for
# the same inputs is reused, a miss runs the suite, a verification failure
# exits 5 and is never swallowed. Result, report and run record land in
# .aval/SUITE-{result,report,record}.json. AVAL_POLICY names the trust policy;
# AVAL_CI_RUN_URL and AVAL_REPOSITORY, when set, go into the run record.
#
# With neither set (a contributor with nothing configured) it runs the
# contract's runner.command directly: no lookup, nothing recorded. That is the
# same command aval would run, so the suite is the same; it skips aval because
# a contract's environment.probe (platform, toolchain) fails a run on a machine
# unlike CI (exit 1, "result not recorded").
#
# AVAL_PUSH=1 (local only) signs the result keyless and pushes it to
# AVAL_REGISTRY, after the suite passed and only from a clean worktree: an
# untracked or modified file would be fingerprinted but never tested by a
# script that clones HEAD, and a failing result would win ties against passing
# ones. CI never sets it; there a separate job signs what the suite jobs recorded.
# AVAL_PUSH=recorded does the same for the result a previous run left in .aval/
# without running the suite again (make aval-local: .github/aval/local.sh).
set -euo pipefail
suite=${1:?usage: run.sh SUITE}
# A runner.command that reaches run.sh again (a wrapper make target, env ...
# make, bash -c "make ...") would loop forever.
[ -z "${PACTO_AVAL_SUITE:-}" ] || { echo "aval: suite $suite started inside suite $PACTO_AVAL_SUITE: runner.command must never reach run.sh again" >&2; exit 2; }
export PACTO_AVAL_SUITE=$suite
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"
contract="aval.$suite.yaml"
[ -f "$contract" ] || { echo "aval: $contract not found" >&2; exit 2; }

# runner.command is written on one line as a flow list of double-quoted words
# with no comma or quote inside (tests/architecture checks it), so it splits
# without a YAML parser.
inner=$(sed -n 's/^  command: \[\(.*\)\]$/\1/p' "$contract")
[ -n "$inner" ] || { echo "aval: $contract: runner.command must be one line: command: [\"word\", ...]" >&2; exit 2; }
cmd=()
IFS=',' read -ra words <<< "$inner"
for w in "${words[@]}"; do
  w="${w#"${w%%[![:space:]]*}"}" w="${w%"${w##*[![:space:]]}"}"
  [[ "$w" =~ ^\"[^\"]+\"$ ]] || { echo "aval: $contract: runner.command word $w is not a double-quoted string" >&2; exit 2; }
  w="${w#\"}" cmd+=("${w%\"}")
done

if [ "${GITHUB_ACTIONS:-}" != true ] && [ -z "${AVAL_REGISTRY:-}" ]; then
  exec "${cmd[@]}"
fi

push=${AVAL_PUSH:-}
case "$push" in "" | 1 | recorded) ;; *) echo "aval: AVAL_PUSH must be 1 or recorded, got $push" >&2; exit 2 ;; esac
if [ -n "$push" ]; then
  [ "${GITHUB_ACTIONS:-}" != true ] || { echo "aval: AVAL_PUSH is local only; CI signs recorded results in its own job" >&2; exit 2; }
  [ -n "${AVAL_REGISTRY:-}" ] || { echo "aval: AVAL_PUSH=1 needs AVAL_REGISTRY" >&2; exit 2; }
  if [ -n "$(git status --porcelain)" ]; then
    echo "aval: AVAL_PUSH=1 refuses to sign from a worktree with changes; commit or stash them:" >&2
    git status --short >&2
    exit 2
  fi
  # delivery.sh and delivery-demo.sh load the Pacto images from this archive
  # instead of building them, and only compare tags (the release version): an
  # archive from another tree would be tested and signed as this one.
  [ -z "${DELIVERY_LOAD_IMAGES:-}" ] || { echo "aval: AVAL_PUSH=1 refuses DELIVERY_LOAD_IMAGES: the loaded images are not built from this tree" >&2; exit 2; }
fi

version=$(sed -n 's/^AVAL_VERSION=//p' .github/aval/install.sh)
aval=${AVAL:-$root/.aval/aval-$version/aval}
[ -x "$aval" ] || bash .github/aval/install.sh

# A modified tracked file is fingerprinted here but not in aval-publish, which
# checks out the commit clean and would refuse the result.
if [ "${GITHUB_ACTIONS:-}" = true ] && [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "::warning title=aval ($suite)::tracked files changed before the suite ran; aval-publish will compute another fingerprint: $(git status --porcelain --untracked-files=no | tr '\n' ' ')"
fi
mkdir -p .aval
result=".aval/$suite-result.json" report=".aval/$suite-report.json"
args=(run -f "$contract" --result-file "$result" --report-file "$report" --stats-file ".aval/$suite-record.json")
[ -z "${AVAL_CI_RUN_URL:-}" ] || args+=(--ci-run-url "$AVAL_CI_RUN_URL")
[ -z "${AVAL_REPOSITORY:-}" ] || args+=(--source-repo "$AVAL_REPOSITORY")
rc=0
if [ "$push" = recorded ]; then
  [ -f "$report" ] || { echo "aval: $suite: no $report from a previous run: nothing to push" >&2; exit 1; }
else
  "$aval" "${args[@]}" || rc=$?
fi
# aval exits 0 when it reuses a verified result even if another candidate for
# the same fingerprint failed verification (it lists it under rejected), and
# when a flooded fingerprint made it run the test. Both are a security event in
# the registry: fail like exit 5.
if [ "$rc" = 0 ] && [ -f "$report" ] && jq -e -f .github/aval/security.jq "$report" >/dev/null; then
  echo "aval: SECURITY: a security event for $suite (verification failure or flooding, see $report); failing as a verification failure" >&2
  rc=5
fi
[ -n "$push" ] && [ "$rc" = 0 ] || exit "$rc"

# What no policy reuses (pr.yaml and main.yaml list no allowedCacheability) is
# not pushed: it adds nothing, as in publish.sh.
if grep -Eq '^cacheability: (non-deterministic|external-dependencies)' "$contract"; then
  echo "aval: $suite is $(sed -n 's/^cacheability: //p' "$contract"), never reused: nothing to push"
elif jq -e '.outcome == "verified"' "$report" >/dev/null; then
  echo "aval: $suite reused a verified result: nothing to push"
elif jq -e '.outcome == "error"' "$report" >/dev/null; then
  # A lookup error is not a miss (publish.sh skips it on pull requests too):
  # pushing anyway piles attestations toward aval's flooding limit.
  echo "aval: $suite: the lookup failed (see $report): nothing pushed" >&2
  exit 1
elif [ -f "$result" ] && jq -e '.result == "passed"' "$result" >/dev/null; then
  "$aval" push -f "$contract" --registry "$AVAL_REGISTRY" --result-file "$result"
else
  echo "aval: $suite did not pass: nothing pushed" >&2
  exit 1
fi
