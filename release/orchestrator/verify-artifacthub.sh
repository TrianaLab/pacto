#!/usr/bin/env bash
# verify-artifacthub.sh — round-trip check for the Artifact Hub repository-metadata
# artifact (release-safety item 12).
#
# IMPORTANT: the `...:artifacthub.io` tag is an INTENTIONAL MUTABLE repo-level
# ALIAS, NOT an immutable SemVer chart version. Artifact Hub reads repository
# ownership metadata from this one well-known tag, so every release RE-PUSHES the
# same repo metadata to it. A re-push + round-trip is therefore EXPECTED to match
# byte-for-byte — this is the opposite of the versioned chart tags, which are
# immutable and must never be overwritten (see verify-oci.sh).
#
# Pulls the pushed artifact and asserts, against the local metadata file:
#   - the layer media type is the Artifact Hub repository-metadata type,
#   - repositoryID matches,
#   - owners match,
#   - the file content is byte-identical.
# Exits non-zero on any mismatch.
#
#   verify-artifacthub.sh <ref> <local-metadata-file>
set -euo pipefail
REF="${1:?oci ref required (…/charts/<chart>:artifacthub.io)}"
LOCAL="${2:?local artifacthub-repo.yml required}"
MT='application/vnd.cncf.artifacthub.repository-metadata.layer.v1.yaml'
test -f "$LOCAL" || { echo "::error::local metadata file not found: $LOCAL" >&2; exit 1; }

orasFlags() { case "$REF" in localhost*|127.0.0.1*) printf -- '--plain-http';; esac; }
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT

# --- media type: the pushed layer must carry the Artifact Hub repo-metadata type.
# shellcheck disable=SC2046  # $(orasFlags) is intentionally unquoted: empty => no arg.
manifest="$(oras manifest fetch $(orasFlags) "$REF")"
echo "$manifest" | jq -e --arg mt "$MT" 'any(.layers[]; .mediaType==$mt)' >/dev/null \
  || { echo "::error::${REF}: no layer with media type ${MT}; layer types were: $(echo "$manifest" | jq -r '[.layers[].mediaType]|join(",")')" >&2; exit 1; }

# --- content: pull the artifact and diff the round-tripped file against local bytes.
# shellcheck disable=SC2046
oras pull $(orasFlags) "$REF" -o "$WORK" >/dev/null
pulled="$WORK/$(basename "$LOCAL")"
test -f "$pulled" || { echo "::error::${REF}: pulled artifact has no $(basename "$LOCAL")" >&2; exit 1; }

# --- repositoryID + owners: explicit field checks (redundant with the byte diff
# below, but give a precise failure if the metadata schema ever changes shape).
repo_id() { grep -E '^[[:space:]]*repositoryID:' "$1" | head -n1 | sed -E 's/^[[:space:]]*repositoryID:[[:space:]]*//; s/[[:space:]]*$//'; }
owners_block() { awk '/^owners:/{f=1} f&&/^[^[:space:]#]/&&!/^owners:/{f=0} f' "$1"; }
[ "$(repo_id "$LOCAL")" = "$(repo_id "$pulled")" ] \
  || { echo "::error::${REF}: repositoryID differs (local='$(repo_id "$LOCAL")' pulled='$(repo_id "$pulled")')" >&2; exit 1; }
diff <(owners_block "$LOCAL") <(owners_block "$pulled") >/dev/null \
  || { echo "::error::${REF}: owners differ from ${LOCAL}" >&2; exit 1; }

# --- exact content: the round-tripped file is byte-identical to the local source.
diff -u "$LOCAL" "$pulled" || { echo "::error::${REF}: artifacthub metadata content differs from ${LOCAL}" >&2; exit 1; }

echo "${REF}: artifacthub metadata round-trip OK (media type + repositoryID + owners + exact content match ${LOCAL})"
