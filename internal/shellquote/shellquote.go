// Package shellquote renders command-line tokens a person pastes into a
// POSIX shell.
package shellquote

import "strings"

// Quote renders tok as exactly one POSIX shell word. A token made only of
// characters the shell reads literally is returned unchanged, so the ordinary
// line stays as readable as it was; anything else is single-quoted, with an
// inner quote spelled the only way single quotes allow.
//
// C0 controls and DEL are STRIPPED rather than quoted. A quoted newline is
// still a newline, and several terminals submit a pasted line the moment they
// see one, so there is no rendering of a control character that is both honest
// and safe.
func Quote(tok string) string {
	tok = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, tok)
	if tok != "" && !strings.ContainsFunc(tok, needsShellQuote) {
		return tok
	}
	return "'" + strings.ReplaceAll(tok, "'", `'\''`) + "'"
}

// needsShellQuote reports whether r has any meaning to a POSIX shell. The
// allowed set is the conservative one: unreserved ASCII plus the punctuation
// that appears in a ref, a flag or a path, so a bare `pacto fleet get svc
// --local=.` is untouched while everything else is quoted rather than judged.
func needsShellQuote(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case strings.ContainsRune("@%_+=:,./-", r):
		return false
	}
	return true
}
