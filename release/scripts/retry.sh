#!/usr/bin/env bash
# retry.sh COMMAND [ARG...] — run a command, retrying on failure with a linear
# backoff, and exit with the last failure's status once the attempts run out.
#
# Every `go mod download` and `go install` in CI is one network round trip per
# module against proxy.golang.org, and the go command has no retry of its own:
# a single 5xx, a dropped HTTP/2 stream or a stalled connection fails the whole
# step. That is not hypothetical. On 2026-09-11 it half-shipped the 3.3.0/5.4.0
# release (run 34613593980 died fetching one module zip, after the tags were
# already pushed), then inside the next hour it took out `ci-e2e-kind
# (observation)` and `ci-e2e-compose` on a tree whose Go files nobody had
# touched. Three failures, one cause, zero retries anywhere.
#
# The npm side of the same problem is already handled — ci.mk passes
# --fetch-retries=5 to `npm ci`. This is the Go side of it.
#
# A shell script rather than a composite action because the callers are not all
# workflows: ci.mk and release/scripts/verify-standalone.sh need it too, and a
# Makefile cannot `uses:` anything. The two Dockerfiles cannot use it either —
# their build context does not include this directory — so they carry the same
# loop inline, and tests/release/workflow_tooling_test.go holds every one of
# them to it.
set -euo pipefail

if [ "$#" -eq 0 ]; then
  echo "usage: retry.sh COMMAND [ARG...]" >&2
  exit 2
fi

attempts=5
attempt=1
while true; do
  status=0
  "$@" || status=$?
  if [ "$status" -eq 0 ]; then
    exit 0
  fi
  if [ "$attempt" -ge "$attempts" ]; then
    echo "retry: '$*' failed $attempts times; giving up" >&2
    exit "$status"
  fi
  delay=$((attempt * 5))
  echo "retry: attempt $attempt of '$*' exited $status; retrying in ${delay}s" >&2
  sleep "$delay"
  attempt=$((attempt + 1))
done
