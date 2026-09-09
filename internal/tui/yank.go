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
func yankLine(c *Context, sel Selection) string {
	return strings.Join(yankArgv(c, sel), " ")
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
