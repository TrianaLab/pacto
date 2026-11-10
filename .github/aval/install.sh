#!/usr/bin/env bash
# Usage: install.sh (make aval-install). Installs the pinned aval release into
# <repo>/.aval/aval-<version>/aval (.aval/ is gitignored and never hashed by
# aval), the binary every make target runs. The same script installs it on a
# laptop and in CI. It is verified before it is moved into place:
#   - the archive must have the sha256 pinned below, so a re-tagged release
#     cannot change what runs;
#   - checksums.txt must carry a keyless Sigstore signature from TrianaLab/aval's
#     release workflow at that tag;
#   - checksums.txt must list the archive with the same sha256.
# Needs curl, tar, sha256sum or shasum, and cosign (sigstore/cosign-installer in
# CI, `brew install cosign` locally). Any failure exits non-zero before aval is
# installed. Bump AVAL_VERSION and the four sha256 values together, from the
# release's checksums.txt.
set -euo pipefail
AVAL_VERSION=0.7.0
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64)                platform=linux_amd64  sha=ca69a546f50902f12a7b22f65f520f3b9ed6ec296bdc576085aff35aa94102d8 ;;
  Linux/aarch64 | Linux/arm64) platform=linux_arm64  sha=a3507bd1196aee616fbd083b3407f411f526534cdf942d475bd076f20511387d ;;
  Darwin/x86_64)               platform=darwin_amd64 sha=ca1a365a04e805c5fff0c81ad4136df22fdecc956a10b82e1909255f4c3bb1ed ;;
  Darwin/arm64)                platform=darwin_arm64 sha=9f7e62dca94f2bdb49e5e3a1041e338d732953aa9b22b8f52f0881969ebc5142 ;;
  *) echo "install.sh: no pinned aval release for $(uname -s)/$(uname -m)" >&2; exit 1 ;;
esac
tag="v$AVAL_VERSION" archive="aval_${AVAL_VERSION}_${platform}.tar.gz"
dest="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/.aval/aval-$AVAL_VERSION"
if command -v sha256sum >/dev/null; then sum=(sha256sum); else sum=(shasum -a 256); fi
command -v cosign >/dev/null || { echo "install.sh: cosign is required to verify the aval release (https://docs.sigstore.dev/cosign/system_config/installation/)" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"
for f in "$archive" checksums.txt checksums.txt.sigstore.json; do
  curl -fsSL --retry 5 --retry-all-errors -o "$f" "https://github.com/TrianaLab/aval/releases/download/$tag/$f"
done
echo "$sha  $archive" | "${sum[@]}" -c -
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity "https://github.com/TrianaLab/aval/.github/workflows/release.yaml@refs/tags/$tag"
grep -qxF "$sha  $archive" checksums.txt || { echo "install.sh: checksums.txt does not list $archive with the pinned sha256" >&2; exit 1; }
tar -xzf "$archive" aval
mkdir -p "$dest"
mv aval "$dest/aval"
"$dest/aval" --version
