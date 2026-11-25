package shellquote

import "testing"

// TestQuoteHandlesTheAwkwardTokens covers the three shapes the quoter has
// to get exactly right and cannot get from a fixture: the inner single quote,
// the empty token and the control character that is stripped rather than quoted.
func TestQuoteHandlesTheAwkwardTokens(t *testing.T) {
	for _, tt := range []struct{ name, in, want string }{
		{"a plain token is left alone", "pacto", "pacto"},
		{"a ref keeps its punctuation", "oci://ghcr.io/acme/svc:1.0.0", "oci://ghcr.io/acme/svc:1.0.0"},
		{"a flag with a value is left alone", "--local=.", "--local=."},
		{"a shell metacharacter is quoted", "a;b", `'a;b'`},
		{"an inner quote is spelled the only way single quotes allow", "it's", `'it'\''s'`},
		{"an empty token still occupies one argument", "", `''`},
		{"a newline is stripped, not quoted", "a\nb", "ab"},
		{"a carriage return and a NUL are stripped too", "a\r\x00b", "ab"},
		{"a token that is only control characters collapses to an empty word", "\x01\x02", `''`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Quote(tt.in); got != tt.want {
				t.Fatalf("Quote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
