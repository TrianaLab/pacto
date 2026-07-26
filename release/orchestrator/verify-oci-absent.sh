#!/usr/bin/env bash
# Fail-closed immutability guard for an OCI ref (release/DESIGN-release-safety.md).
# Exits non-zero if the versioned tag already exists — a fresh publish must never
# overwrite an immutable version. Recovery of a genuinely incomplete unit never
# reaches here (its tag was never pushed). Uses whichever registry client the
# calling job has (docker / crane / oras).
set -euo pipefail
REF="${1:?oci ref required}"

exists() {
  if command -v crane >/dev/null 2>&1; then crane digest "$REF" >/dev/null 2>&1
  elif command -v oras >/dev/null 2>&1; then oras manifest fetch "$REF" >/dev/null 2>&1
  else docker manifest inspect "$REF" >/dev/null 2>&1
  fi
}

if exists; then
  echo "::error::${REF} already exists — refusing to overwrite an immutable version (recover via workflow_dispatch if this unit is genuinely incomplete)"
  exit 1
fi
echo "${REF} is absent — safe to publish"
