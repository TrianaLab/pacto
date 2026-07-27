#!/usr/bin/env bash
# Finalize the GitHub Release LAST, after every
# other unit for the transaction has published, so a finalized Release never
# advertises missing assets. Fail-closed immutability: an existing release with
# different asset checksums is a violation (never alter an immutable release); an
# existing release with matching checksums is an idempotent resume.
set -euo pipefail
TAG="${1:?tag required}"
SHA="${2:?sha required}"
OUT="${PACTO_DIST_DIR:-dist}"

if gh release view "$TAG" >/dev/null 2>&1; then
  tmp="$(mktemp -d)"
  if gh release download "$TAG" -p checksums.txt -D "$tmp" 2>/dev/null; then
    if ! diff -q "$tmp/checksums.txt" "${OUT}/checksums.txt" >/dev/null; then
      echo "::error::release ${TAG} already has different asset checksums — refusing to alter an immutable release"
      exit 1
    fi
    echo "release ${TAG} already finalized with matching assets — nothing to do"
  else
    echo "release ${TAG} exists without checksums — uploading assets"
    gh release upload "$TAG" "${OUT}"/*
  fi
else
  gh release create "$TAG" --title "$TAG" --target "$SHA" --generate-notes "${OUT}"/*
fi
echo "finalized release ${TAG}"
