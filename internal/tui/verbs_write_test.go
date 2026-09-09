package tui

import (
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// verbByKey returns the write verb bound to key, failing if it is missing. A
// missing verb is a real failure rather than a skip: the table is the only
// definition of what the TUI can do, so a key that has fallen out of it is a
// capability that silently disappeared.
func verbByKey(t *testing.T, key string) Verb {
	t.Helper()
	for _, v := range writeVerbs() {
		if v.Key == key {
			return v
		}
	}
	t.Fatalf("no write verb bound to %q", key)
	return Verb{}
}

// TestEveryWriteVerbOpensAConfirmation is the guarantee that matters most about
// this table: there is no key that changes anything without asking first. It
// drives every verb through its own Applies predicate, so a verb whose predicate
// and prompt disagree fails here rather than at a reader's terminal.
func TestEveryWriteVerbOpensAConfirmation(t *testing.T) {
	local := Selection{
		Kind: fleet.KindRevision, Key: "local@1.0.0", Label: "local 1.0.0",
		Ref: "/tmp/local", Local: true,
	}
	remote := Selection{
		Kind: fleet.KindRevision, Key: "remote@1.0.0", Label: "remote 1.0.0",
		Ref: "ghcr.io/acme/remote:1.0.0", Local: false,
	}

	for _, tt := range []struct {
		key string
		sel Selection
	}{
		{"p", local},
		{"P", remote},
		{"L", local},
		{"G", local},
	} {
		t.Run(tt.key, func(t *testing.T) {
			c := newLoadedContext(t)
			v := verbByKey(t, tt.key)
			if !v.Applies(tt.sel) {
				t.Fatalf("%q does not apply to the selection it is meant for", tt.key)
			}
			cmd := v.Run(c, tt.sel)
			msg, ok := cmd().(pushMsg)
			if !ok {
				t.Fatalf("%q produced %T, want a pushMsg opening the confirmation", tt.key, cmd())
			}
			if msg.s.Title() != "Confirm" {
				t.Fatalf("%q pushed %q, want the confirmation screen", tt.key, msg.s.Title())
			}
		})
	}
}

// TestWriteVerbApplicability pins which selections each write verb offers
// itself for. Pull on a directory that is already here and generate against a
// registry reference are both nonsense, and generate is the one that would run
// plugin binaries if it got this wrong.
func TestWriteVerbApplicability(t *testing.T) {
	for _, tt := range []struct {
		name      string
		sel       Selection
		wantPull  bool
		wantLocal bool
	}{
		{"a registry reference", Selection{Ref: "ghcr.io/acme/svc:1.0"}, true, false},
		{"a local directory", Selection{Ref: "/tmp/svc", Local: true}, false, true},
		{"no bundle at all", Selection{}, false, false},
		{"no bundle, local set", Selection{Local: true}, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasRemoteRef(tt.sel); got != tt.wantPull {
				t.Errorf("hasRemoteRef = %v, want %v", got, tt.wantPull)
			}
			if got := hasLocalBundle(tt.sel); got != tt.wantLocal {
				t.Errorf("hasLocalBundle = %v, want %v", got, tt.wantLocal)
			}
		})
	}
}
