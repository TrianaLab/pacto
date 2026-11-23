#!/usr/bin/env bash
# Vendor the upstream Crossplane chart the Pacto chart installs under
# idp.crossplane.install, rendered once with its own defaults into
# files/crossplane/crossplane.yaml.
#
#   hack/vendor-crossplane.sh <version> [<out dir>]
#
# Crossplane is not a chart dependency: Helm enables a dependency whose
# condition path is missing, and `helm upgrade --reuse-values` from a release
# with no `idp` key carries no such path, so a subchart would install
# Crossplane on an upgrade nobody asked for. templates/idp/crossplane.yaml
# emits this file only when idp.crossplane.install is true, with the release
# name and namespace put back in place of the placeholders below. VERSION,
# written last, holds the sha256 of the file so a hand edit shows.
set -euo pipefail

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
	echo "usage: $0 <version> [<out dir>]" >&2
	exit 2
fi
version=$1
out=${2:-"$(cd "$(dirname "$0")/.." && pwd)/charts/pacto-operator/files/crossplane"}
repo=https://charts.crossplane.io/stable

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

helm pull crossplane --repo "$repo" --version "$version" --destination "$work"
# The placeholders are valid release names that occur nowhere upstream; the
# template replaces them with .Release.Name and .Release.Namespace.
helm template xreleasenamex "$work/crossplane-$version.tgz" --namespace xreleasenamespacex |
	grep -v '^# Source: ' >"$work/crossplane.yaml"
if grep -q '{{' "$work/crossplane.yaml"; then
	echo "the render holds a template action; the chart emits it verbatim" >&2
	exit 1
fi

mkdir -p "$out"
mv "$work/crossplane.yaml" "$out/crossplane.yaml"
{
	echo "version: $version"
	echo "source: $repo crossplane $version"
	(cd "$out" && sha256 crossplane.yaml)
} >"$out/VERSION"
echo "vendored Crossplane $version into $out"
