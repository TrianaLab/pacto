package ci

import (
	"regexp"
	"strings"
)

// urlPattern finds the URLs inside a source name or an error message.
var urlPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"']+`)

// Redact drops the userinfo, the query and the fragment of every URL in s,
// which may hold credentials, and marks each URL it shortened "(redacted)".
// A plan's summary can be public, so every source name and error passes
// through it. It reads the text, never a parse: a URL net/url rejects (a bad
// escape, a bad port) is redacted all the same.
func Redact(s string) string {
	return urlPattern.ReplaceAllStringFunc(s, func(raw string) string {
		scheme, rest, _ := strings.Cut(raw, "://")
		end := len(rest)
		if i := strings.IndexAny(rest, "?#"); i >= 0 {
			end = i
		}
		authEnd := strings.IndexByte(rest[:end], '/')
		if authEnd < 0 {
			authEnd = end
		}
		start := strings.LastIndexByte(rest[:authEnd], '@') + 1
		if start == 0 && end == len(rest) {
			return raw
		}
		return scheme + "://" + rest[start:end] + " (redacted)"
	})
}
