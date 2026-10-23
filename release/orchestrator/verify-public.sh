#!/usr/bin/env bash
# Every GHCR repository the transaction published to must be readable anonymously.
#
# GitHub creates a new container package as PRIVATE. The release itself cannot
# notice: every publish, verify, record and sign step runs with GITHUB_TOKEN, so a
# brand-new package passes all of them while `helm install` and `docker pull` fail
# for everyone else. This asks GHCR the way an anonymous user does: an anonymous
# pull token, then a tag listing. Anything but 200 fails the run.
#
# The first release of a new package is expected to fail here. Make the package
# public (and link it to the repository), then re-run this job. See
# the verify-public.sh entry in release/README.md.
#
# Usage: verify-public.sh <units-dir>
#   UNITS  JSON array of the transaction's changed units
#   PACTO_VERIFY_PUBLIC_LIST_ONLY=1  print "<unit> <repository>" for each
#          repository it would query and exit, offline. PR CI asserts this list
#          (tests/release/verify_public_test.go) because the real run only
#          happens inside a release.
set -euo pipefail

units_dir="${1:?usage: verify-public.sh <units-dir>}"
: "${UNITS:?UNITS (units_json) is required}"

failed=0
for pkg in "$units_dir"/*/package.json; do
  read -r unit kind coord < <(jq -r '[.pacto.releaseUnit, .pacto.kind, .pacto.coordinate] | @tsv' "$pkg")
  printf '%s' "$UNITS" | jq -e --arg u "$unit" 'index($u)' >/dev/null || continue
  case "$kind" in oci-image|helm-chart) ;; *) continue ;; esac
  # ponytail: demo-bundles' coordinate is a namespace prefix, not one repository;
  # list its repositories here if they ever need the same check.
  [ "$unit" = demo-bundles ] && continue
  if [ -z "$coord" ] || [ "$coord" = null ]; then
    echo "::error::unit '$unit' ($pkg) has no .pacto.coordinate"
    failed=1
    continue
  fi
  repo="${coord#ghcr.io/}"
  if [ "${PACTO_VERIFY_PUBLIC_LIST_ONLY:-}" = 1 ]; then
    echo "$unit $repo"
    continue
  fi
  # GHCR refuses the anonymous token itself (403) for a private repository.
  code="token-denied"
  if token="$(curl -fsS --connect-timeout 10 --max-time 30 --retry 3 "https://ghcr.io/token?scope=repository:${repo}:pull" | jq -er .token)"; then
    code="$(curl -sS --connect-timeout 10 --max-time 30 --retry 3 -o /dev/null -w '%{http_code}' \
      -H "Authorization: Bearer ${token}" "https://ghcr.io/v2/${repo}/tags/list")"
  fi
  if [ "$code" = 200 ]; then
    echo "  ok: $unit -> $coord is public"
  else
    echo "::error::unit '$unit': anonymous read of $coord returned $code. A new GHCR package starts private: make it public and link it to the repository, then re-run this job."
    failed=1
  fi
done

[ "$failed" -eq 0 ] || exit 1
[ "${PACTO_VERIFY_PUBLIC_LIST_ONLY:-}" = 1 ] && exit 0
echo "every published package is anonymously readable"
