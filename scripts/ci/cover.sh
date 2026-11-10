#!/usr/bin/env bash
# Usage: cover.sh PROFILE PATTERN... Unit tests with the race detector over the
# packages PATTERN matches, minus the test helpers (/testutil) and the eBPF
# loader (it loads a program into the kernel: make test-ebpf and the kind
# runtime suites cover it), with total coverage at 100.0%. The gate is inside
# the suite on purpose: aval.unit.yaml runs this, and a reused result writes no
# profile. COVER_PKGS in ci.mk (make coverage) is the same package set.
set -euo pipefail
profile=${1:?usage: cover.sh PROFILE PATTERN...}
shift
pkgs=$(go list "$@" | grep -v /testutil | grep -v /internal/runtimewatch/ebpf)
echo "==> Running unit tests with race detector and coverage..."
# shellcheck disable=SC2086 # one package per word
go test -race $pkgs -coverprofile="$profile"
total=$(go tool cover -func="$profile" | awk '/^total:/ {print $NF}')
if [ "$total" != "100.0%" ]; then
  echo "FAIL: total coverage is $total, expected 100.0%"
  go tool cover -func="$profile" | grep -v '100.0%'
  exit 1
fi
echo "    total coverage: 100.0%"
