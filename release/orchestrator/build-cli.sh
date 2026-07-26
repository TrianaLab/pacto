#!/usr/bin/env bash
# Reproducible CLI build + checksums + SBOM (release/DESIGN-release-safety.md).
# The build date is derived from the SOURCE COMMIT (committer date), never the
# wall clock, so a re-run of the same transaction produces byte-identical binaries
# — a prerequisite for digest-based immutability + safe resume. Shared by
# release.yml + the dry-run.
set -euo pipefail
TAG="${1:?tag required}"
SHA="${2:?sha required}"
OUT="${PACTO_DIST_DIR:-dist}"

DATE="$(git show -s --format=%cI "$SHA")"   # deterministic committer date
mkdir -p "$OUT"

for pair in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  GOOS="${pair%/*}"; GOARCH="${pair#*/}"
  out="pacto_${GOOS}_${GOARCH}"; [ "$GOOS" = windows ] && out="${out}.exe"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath \
    -ldflags "-s -w -X main.version=${TAG} -X main.gitCommit=${SHA} -X main.buildDate=${DATE}" \
    -o "${OUT}/${out}" ./cmd/pacto
done

( cd "$OUT" && sha256sum pacto_* > checksums.txt )
if command -v syft >/dev/null 2>&1; then
  syft "dir:${OUT}" -o "spdx-json=${OUT}/pacto-sbom.spdx.json"
else
  echo "syft not found — SBOM skipped" >&2
fi
echo "built $(ls "$OUT" | grep -c '^pacto_') binaries for ${TAG} (date ${DATE})"
