#!/usr/bin/env sh
# The blocking U+00A7 gate: the forbidden section-sign glyph must appear NOWHERE in
# authored repository content or PR metadata. Write "section" instead. It scans, in
# separate modes:
#
#   (no args)                 every tracked authored file, INCLUDING committed
#                             generated documentation. Only genuinely non-authored
#                             paths are excluded (see EXCLUDES below); binary files
#                             are skipped by grep -I.
#   FILE...                   exactly the given files (used by the fixture test).
#   --commits RANGE           the commit messages (subject + body) of every commit
#                             in the git RANGE (e.g. origin/main..HEAD). On a hit it
#                             reports the offending commit sha and subject.
#   --text LABEL [FILE]       an arbitrary text (a PR title or body); FILE defaults
#                             to stdin ("-"). On a hit it reports LABEL:line:content.
#
# In GitHub Actions the caller passes the event payload's base..head range and the
# PR title/body. Locally, pass an explicit range (or use the default files mode).
#
# EXCLUDES (narrow and documented): generated third-party UI bundles under
# pkg/dashboard/ui/assets/ carry the glyph as vendor data (KaTeX/Mermaid) and are
# not authored content; binary files (logo/favicon) carry it as opaque bytes and
# are skipped by grep -I.
set -eu

sign=$(printf '\302\247') # UTF-8 bytes for U+00A7

fail() {
	echo "check-section: U+00A7 (section sign) found; write 'section' instead:" >&2
	printf '%s\n' "$1" >&2
	exit 1
}

case "${1:-}" in
--commits)
	range="${2:?usage: check-section-sign.sh --commits <range>}"
	hits=""
	for c in $(git rev-list "$range"); do
		if git log -1 --format='%B' "$c" | grep -qF "$sign"; then
			hits="${hits}commit ${c}: $(git log -1 --format='%s' "$c")
"
		fi
	done
	[ -n "$hits" ] && fail "$hits"
	echo "check-section: zero U+00A7 in commit messages of $range"
	;;
--text)
	label="${2:?usage: check-section-sign.sh --text <label> [file]}"
	file="${3:--}"
	if [ "$file" = "-" ]; then
		content=$(cat)
	else
		content=$(cat "$file")
	fi
	hit=$(printf '%s\n' "$content" | grep -nF "$sign" | sed "s#^#${label}:#" || true)
	[ -n "$hit" ] && fail "$hit"
	echo "check-section: zero U+00A7 in $label"
	;;
*)
	if [ "$#" -gt 0 ]; then
		files=$(printf '%s\n' "$@")
	else
		files=$(git ls-files | grep -v '^pkg/dashboard/ui/assets/')
	fi
	hits=$(printf '%s\n' "$files" | grep -v '^$' | xargs grep -IHnF "$sign" 2>/dev/null || true)
	[ -n "$hits" ] && fail "$hits"
	echo "check-section: zero U+00A7 in authored files (generated UI assets excluded by path)"
	;;
esac
