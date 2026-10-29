#!/usr/bin/env bash
# Decides whether docs.yml may redeploy the site from main without a release.
#
# Usage: docs_publish_gate.sh <git-ref-holding-versions.json | path/to/versions.json>
# Run from the repository root. Writes publish=true|false and version=<core> to
# $GITHUB_OUTPUT and the reason to $GITHUB_STEP_SUMMARY (stdout when unset).
#
# Publishes only when main has no pending changesets AND the core version in
# release/release-manifest.json already carries the `latest` alias in versions.json.
# The second condition is what keeps the version selector unchanged: mike can only
# overwrite a slot that exists, and only a release transaction creates one.
set -euo pipefail

src=${1:?usage: docs_publish_gate.sh <gh-pages ref | versions.json path>}
out=${GITHUB_OUTPUT:-/dev/null}
summary=${GITHUB_STEP_SUMMARY:-/dev/stdout}

skip() {
  echo "publish=false" >>"$out"
  printf '### Docs not published\n\n%s\n' "$1" >>"$summary"
  exit 0
}

pending=$(find .changeset -maxdepth 1 -name '*.md' ! -name README.md | sort)
if [ -n "$pending" ]; then
  skip "main carries unreleased changes (pending changesets: $(echo "$pending" | xargs -n1 basename | paste -sd' ' -)). A docs fix needs a \`\"@pacto/core\": patch\` changeset and ships with the next release."
fi

version=$(jq -er '.units.core.version' release/release-manifest.json)

if [ -f "$src" ]; then
  versions=$(cat "$src")
else
  versions=$(git show "$src:versions.json")
fi

if ! jq -e --arg v "$version" 'any(.[]; .version == $v and (.aliases | index("latest")))' <<<"$versions" >/dev/null; then
  skip "core $version does not hold the \`latest\` alias in versions.json yet. A release is in flight and publishes the site itself; this job never adds a version entry."
fi

echo "publish=true" >>"$out"
echo "version=$version" >>"$out"
# shellcheck disable=SC2016 # the backticks are Markdown, not a command
printf '### Docs published\n\nRedeployed main over the released core %s (`latest`).\n' "$version" >>"$summary"
