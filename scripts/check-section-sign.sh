#!/usr/bin/env sh
# Fails if the forbidden U+00A7 section-sign appears in any AUTHORED, tracked file.
# Write "section" instead. Generated third-party UI bundles under
# pkg/dashboard/ui/assets/ are excluded by path: they carry the glyph as vendor
# data (KaTeX/Mermaid) and are not authored content.
set -eu

sign=$(printf '\302\247') # UTF-8 bytes for U+00A7

hits=$(git ls-files \
	| grep -v '^pkg/dashboard/ui/assets/' \
	| xargs grep -IlF "$sign" 2>/dev/null || true)

if [ -n "$hits" ]; then
	echo "check-section: U+00A7 (section sign) found in authored files; write 'section' instead:" >&2
	echo "$hits" >&2
	exit 1
fi

echo "check-section: zero U+00A7 in authored files (generated UI assets excluded by path)"
