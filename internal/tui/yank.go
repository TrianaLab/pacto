package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// clipboardWrite copies a line to the system clipboard. It is nil by default:
// the TUI adds no clipboard dependency, and showing the line is a perfectly
// good fallback that also works over ssh. A host that wants real copying sets
// this at build time.
var clipboardWrite func(string) error

// yankArgv is the single definition of what y copies. yankLine and the verb's
// Argv both come from here, so the line shown, the line copied and the line the
// help screen advertises cannot drift.
func yankArgv(c *Context, sel Selection) []string {
	if sel.Ref != "" {
		// A bundle-backed selection yanks the command that says the most about
		// it and is safe to run: validate. The reader edits the verb, not the
		// path, which is the part the TUI saved them from typing.
		//
		// No source flags here: validate reads one bundle off disk and takes no
		// fleet at all, so a --local on this line is a parse error.
		return []string{"pacto", "validate", sel.Ref}
	}
	// Without a bundle the line has to be a fleet query, and each kind is named
	// differently: fleet get takes a service positional or --target, fleet graph
	// takes a service positional, --revision or --target.
	//
	// Every one of them rebuilds a snapshot, and with no source flags it rebuilds
	// it from the defaults -- which answers "not found" for everything on screen
	// the moment the session was launched with a non-default source. So each line
	// carries the flags the reader actually typed.
	switch sel.Kind {
	case fleet.KindRevision:
		// get resolves a service and explain tries a service then a target
		// (pkg/fleet/query.go:908); graph is the only one of the three that will
		// take a revision key.
		return c.fleetLine("graph", "--revision", sel.Key)
	case fleet.KindTarget:
		return c.fleetLine("get", "--target", sel.Key)
	case fleet.KindOwner:
		// An owner and a source are subjects none of get, graph or explain
		// accept. fleet search is the command that does take them, as a filter.
		// The owner filter matches the value rather than the namespaced key --
		// Owner.MatchesFilter substring-matches Team and DRI
		// (pkg/contract/owner.go:218) -- so it gets Label, where Key still
		// carries the "team:" prefix (pkg/fleet/product.go:200).
		return c.fleetLine("search", "--owner", sel.Label)
	case fleet.KindSource:
		return c.fleetLine("search", "--source", sel.Key)
	}
	return c.fleetLine("get", sel.Key)
}

// fleetLine builds `pacto fleet <sub> <args...>` with the session's source flags
// appended, so the query resolves the snapshot the screen is showing.
func (c *Context) fleetLine(sub string, args ...string) []string {
	argv := append([]string{"pacto", "fleet", sub}, args...)
	return append(argv, c.SourceArgs...)
}

// yankLine returns the invocation the current selection would run. It is how
// the TUI stays a front-end over the CLI rather than a replacement for it: for
// every command the TUI does not offer, the reader gets the exact line to type.
//
// Every token is shell-quoted, because the values in it are untrusted and the
// line's documented purpose is to be pasted into a shell. Nothing between the
// contract on disk and this function validates content: internal/fleetsrc/
// local.go calls contract.Parse and nothing else, so owner.team's
// ^[a-zA-Z0-9._/-]+$ pattern never runs on a fleet load, and k8s.go copies
// custom-resource values through verbatim. Unquoted, an owner of
// "platform;touch /tmp/pwned;true" yanks a line that runs that command, and the
// quieter half of the same bug is that any value with a space in it pastes as
// two arguments plus a stray positional.
//
// The quoting lives here rather than in yankArgv because that argv is also
// Verb.Argv, which execVerb hands to exec.Command directly: no shell is
// involved there, so a quote would become a literal character in the argument
// instead of protecting it. internal/cli's fleetSourceArgs is left alone for
// the same reason.
func yankLine(c *Context, sel Selection) string {
	argv := yankArgv(c, sel)
	quoted := make([]string, len(argv))
	for i, tok := range argv {
		quoted[i] = shellQuote(tok)
	}
	return strings.Join(quoted, " ")
}

// shellQuote renders tok as exactly one POSIX shell word. A token made only of
// characters the shell reads literally is returned unchanged, so the ordinary
// line stays as readable as it was; anything else is single-quoted, with an
// inner quote spelled the only way single quotes allow.
//
// C0 controls and DEL are STRIPPED rather than quoted. A quoted newline is
// still a newline, and several terminals submit a pasted line the moment they
// see one, so there is no rendering of a control character that is both honest
// and safe.
func shellQuote(tok string) string {
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

// verbYank is registered in verbList as the y verb.
func verbYank(c *Context, sel Selection) tea.Cmd {
	line := yankLine(c, sel)
	if clipboardWrite == nil {
		return status(line)
	}
	if err := clipboardWrite(line); err != nil {
		return status("copy failed: " + err.Error())
	}
	return status("copied: " + line)
}
