#!/usr/bin/env sh
# Fails if the forbidden U+00A7 section-sign appears in any AUTHORED, tracked file.
# Write "section" instead. Generated third-party UI bundles under
# pkg/dashboard/ui/assets/ are excluded by path: they carry the glyph as vendor
# data (KaTeX/Mermaid) and are not authored content. Binary files are skipped by
# grep -I (the committed logo/favicon/binary carry the glyph as opaque bytes).
#
# With no arguments it scans every tracked authored file (`git ls-files`). Given
# one or more path arguments it scans exactly those (used by the fixture test).
# On a hit it reports `path:line:content` so the offending glyph is locatable.
set -eu

sign=$(printf '\302\247') # UTF-8 bytes for U+00A7

if [ "$#" -gt 0 ]; then
	files=$(printf '%s\n' "$@")
else
	files=$(git ls-files | grep -v '^pkg/dashboard/ui/assets/')
fi

hits=$(printf '%s\n' "$files" | grep -v '^$' | xargs grep -IHnF "$sign" 2>/dev/null || true)

if [ -n "$hits" ]; then
	echo "check-section: U+00A7 (section sign) found in authored files; write 'section' instead:" >&2
	echo "$hits" >&2
	exit 1
fi

echo "check-section: zero U+00A7 in authored files (generated UI assets excluded by path)"
