#!/usr/bin/env bash
# Decides whether docs.yml may redeploy the site from main without a release.
#
# Usage: docs_publish_gate.sh <git-ref-holding-versions.json | path/to/versions.json>
# Run from the repository root. Writes publish=true|false and version=<core> to
# $GITHUB_OUTPUT and the reason to $GITHUB_STEP_SUMMARY (stdout when unset).
#
# Publishes only when all three hold:
#   1. main has no pending changesets;
#   2. everything changed since the last release commit (the last commit to touch
#      release/release-transaction.json) is authored docs, so `make docs-deploy`
#      regenerates the CLI, integration and demo content from released code. HEAD
#      being that release commit also skips, because release.yml publishes it;
#   3. the core version in release/release-manifest.json already carries the
#      `latest` alias in versions.json. That keeps the version selector unchanged:
#      mike can only overwrite a slot that exists, and only a release creates one.
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

base=$(git log -1 --format=%H -- release/release-transaction.json)
[ -n "$base" ] || skip "no release commit found in the history; release.yml publishes the first release."
if [ "$base" = "$(git rev-parse HEAD)" ]; then
  skip "this commit starts a release transaction; release.yml publishes the docs."
fi
changed=$(git diff --name-only "$base" HEAD)
[ -n "$changed" ] || skip "nothing changed since the release (${base:0:12}); the site already matches it."
# The authored docs: the site tree, its theme overrides and config, and each
# integration's docs. Anything else means main differs from the release in code
# or generated reference, which ships only with a release.
if grep -vqE '^(docs/|overrides/|mkdocs\.yml$|integrations/[^/]+/docs/|\.changeset/README\.md$)' <<<"$changed"; then
  skip "main changed more than docs since the release (${base:0:12}). Ship it with a \`\"@pacto/core\": patch\` changeset."
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
