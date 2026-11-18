package discover

import (
	"net/url"
	"strings"
)

// cleanURL is raw without userinfo, query or fragment, so a credential in a
// URL never reaches a record or a finding. A value that does not parse as a
// URL with a host is cut to the text after its last "@" and before its first
// "?".
// ponytail: a 10-line copy of cleanRunURL's rules (internal/cli, which
// discovery must not import); one shared helper if a third caller appears.
func cleanURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
		return u.String()
	}
	if i := strings.LastIndexByte(raw, '@'); i >= 0 {
		raw = raw[i+1:]
	}
	raw, _, _ = strings.Cut(raw, "?")
	return raw
}
