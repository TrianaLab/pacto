#!/usr/bin/env bash
# Vendor the upstream Flux release manifest the Pacto chart installs under
# idp.flux.install: the source, kustomize and helm controllers and the objects
# they share, one document per file, byte for byte.
#
#   hack/vendor-flux.sh <version> <upgrade from> [<out dir>]
#
# <version> is the Flux release (2.10.1), <upgrade from> the release the kind
# scenario upgrades from (the latest patch of the previous minor). The script
# edits no document: the chart's templates/idp/flux.yaml adds labels, keep and
# the lockdown arguments at render time. VERSION, written last, lists the
# sha256 of every file so make flux-vendor-check refuses a hand edit.
set -euo pipefail

if [ $# -lt 2 ] || [ $# -gt 3 ]; then
	echo "usage: $0 <version> <upgrade from> [<out dir>]" >&2
	exit 2
fi
version=$1
upgrade_from=$2
out=${3:-"$(cd "$(dirname "$0")/.." && pwd)/charts/pacto-operator/files/flux"}
base="https://github.com/fluxcd/flux2/releases/download/v${version}"

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

curl -fsSL -o "$work/install.yaml" "$base/install.yaml"
sum=$(sha256 "$work/install.yaml" | awk '{print $1}')
if curl -fsSL -o "$work/checksums.txt" "$base/flux_${version}_checksums.txt" &&
	want=$(awk '$2 == "install.yaml" {print $1}' "$work/checksums.txt") && [ -n "$want" ]; then
	if [ "$want" != "$sum" ]; then
		echo "install.yaml sha256 $sum does not match the release checksum file ($want)" >&2
		exit 1
	fi
	echo "install.yaml sha256 $sum matches flux_${version}_checksums.txt"
else
	echo "the release checksum file does not list install.yaml: confirm sha256 $sum with a second download" >&2
fi

mkdir -p "$out"
rm -f "$out"/*.yaml "$out/VERSION"

# One document per file, named <kind lowercased>-<metadata.name>.yaml. A
# document whose app.kubernetes.io/component names a controller Pacto does not
# package is dropped, its CRDs included, and so is one that selects only that
# controller's pods (the webhook NetworkPolicy).
awk -v out="$out" -v drop='^(notification-controller|image-reflector-controller|image-automation-controller|source-watcher)$' '
function flush() {
	if (n == 0) return
	if (kind == "" || name == "") { print "a document has no kind or metadata.name" > "/dev/stderr"; exit 1 }
	if (component !~ drop && selects !~ drop) {
		file = out "/" tolower(kind) "-" name ".yaml"
		for (i = 1; i <= n; i++) print lines[i] > file
		close(file)
	}
	n = 0; kind = ""; name = ""; component = ""; selects = ""; meta = 0
}
/^---$/ { flush(); next }
{
	lines[++n] = $0
	if ($0 ~ /^kind: /) kind = substr($0, 7)
	if ($0 ~ /^[^ ]/) meta = ($0 == "metadata:")
	else if (meta && $0 ~ /^  name: /) name = substr($0, 9)
	if (meta && $0 ~ /^    app\.kubernetes\.io\/component: /) { component = $0; sub(/^ *app\.kubernetes\.io\/component: /, "", component) }
	if ($0 ~ /^      app: /) { selects = $0; sub(/^ *app: /, "", selects) }
}
END { flush() }
' "$work/install.yaml"

{
	echo "version: $version"
	echo "source: $base/install.yaml"
	echo "sha256: $sum"
	echo "upgradeFrom: $upgrade_from"
	(cd "$out" && sha256 -- *.yaml)
} >"$work/VERSION"
mv "$work/VERSION" "$out/VERSION"
echo "vendored $(find "$out" -name "*.yaml" | wc -l | tr -d ' ') documents of Flux $version into $out"
