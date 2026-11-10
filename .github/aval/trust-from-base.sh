#!/bin/bash
# Usage: trust-from-base.sh (reads GITHUB_EVENT_NAME, GITHUB_REF, RUNNER_TEMP,
# GITHUB_ENV)
# Sets AVAL_POLICY and AVAL_REGISTRY for the rest of the job, the two values
# .github/aval/run.sh hands to `aval run`. On a push to main they come from the
# checkout, which is main, with main.yaml. On a pull request (any other event
# or ref) both come from origin/main with pr.yaml, never from the checked-out
# ref or the PR's base branch, so a PR that adds its own identity to a policy
# or points the registry elsewhere does not change what its CI trusts. The
# nightly schedule and a manual dispatch exist to exercise the suites, so they
# set neither: nothing is looked up and every suite runs. The registry is the
# one AVAL_REGISTRY value .github/workflows/ci.yml declares; anything else
# fails the job. ci.yml runs main's copy of this script with /bin/bash, and it
# calls every external tool by absolute path, so an earlier step that writes
# GITHUB_PATH cannot shim them. On pull_request the PR's own workflow and
# Makefile still run, so a hostile PR can make its own check green; review of
# .github/, Makefile, ci.mk and aval.*.yaml protects main, and main trusts only
# its own CI (on main, and on same-repository pull requests: see the trust
# condition in main.yaml).
set -euo pipefail
: "${GITHUB_EVENT_NAME:?}" "${RUNNER_TEMP:?}" "${GITHUB_ENV:?}"
case "$GITHUB_EVENT_NAME" in
  schedule | workflow_dispatch)
    echo "aval: $GITHUB_EVENT_NAME run: no trust policy, every suite runs"
    exit 0
    ;;
esac
if [ "$GITHUB_EVENT_NAME" = push ] && [ "${GITHUB_REF:-}" = refs/heads/main ]; then
  src=HEAD policy=main.yaml
else
  # The calling step has fetched origin/main (with retries) before running
  # this script from it.
  src=origin/main policy=pr.yaml
fi
out="$RUNNER_TEMP/aval-policy.yaml"
/usr/bin/git show "$src:.github/aval/$policy" > "$out"
registry=$(/usr/bin/git show "$src:.github/workflows/ci.yml" | /usr/bin/sed -n 's/^ *AVAL_REGISTRY: *//p' | /usr/bin/sort -u)
if ! [[ "$registry" =~ ^[a-z0-9][a-z0-9./:_-]*$ ]]; then
  echo "::error::$src:.github/workflows/ci.yml must declare exactly one AVAL_REGISTRY, got: $registry"
  exit 1
fi
echo "AVAL_POLICY=$out" >> "$GITHUB_ENV"
echo "AVAL_REGISTRY=$registry" >> "$GITHUB_ENV"
echo "trust policy $src:.github/aval/$policy, registry $registry"
