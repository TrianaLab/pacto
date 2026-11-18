package ci

import (
	"net/url"
	"regexp"
)

// urlPattern finds the URLs inside a source name or an error message.
var urlPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"']+`)

// Redact drops the userinfo and the query of every URL in s, which may hold
// credentials, and marks each URL it shortened "(redacted)". A plan's
// summary can be public, so every source name and error passes through it.
func Redact(s string) string {
	return urlPattern.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.User == nil && u.RawQuery == "" {
			return raw
		}
		u.User, u.RawQuery, u.ForceQuery = nil, "", false
		return u.String() + " (redacted)"
	})
}
