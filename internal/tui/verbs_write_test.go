package tui

import (
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestWriteVerbPushOpensConfirmation(t *testing.T) {
	c := newLoadedContext(t)
	// Need a bundle with a ref for push to apply. Use a direct selection.
	sel := Selection{
		Kind:  fleet.KindRevision,
		Key:   "test@1.0.0",
		Label: "test 1.0.0",
		Ref:   "/tmp/test",
		Local: true,
	}
	// Find the push verb and run it.
	for _, v := range writeVerbs() {
		if v.Key == "p" {
			cmd := v.Run(c, sel)
			msg, ok := cmd().(pushMsg)
			if !ok {
				t.Fatalf("p produced %T, want pushMsg", cmd())
			}
			if msg.s.Title() != "Confirm" {
				t.Fatalf("p pushed %q, want Confirm", msg.s.Title())
			}
			return
		}
	}
	t.Fatal("push verb not found")
}

func TestWriteVerbPullOpensConfirmation(t *testing.T) {
	c := newLoadedContext(t)
	// Need a remote ref for pull to apply. Build a selection manually.
	sel := Selection{
		Kind:  fleet.KindRevision,
		Key:   "remote@1.0.0",
		Label: "remote 1.0.0",
		Ref:   "ghcr.io/acme/remote:1.0.0",
		Local: false,
	}
	// Find the pull verb and run it.
	for _, v := range writeVerbs() {
		if v.Key == "P" {
			cmd := v.Run(c, sel)
			msg, ok := cmd().(pushMsg)
			if !ok {
				t.Fatalf("P produced %T, want pushMsg", cmd())
			}
			if msg.s.Title() != "Confirm" {
				t.Fatalf("P pushed %q, want Confirm", msg.s.Title())
			}
			return
		}
	}
	t.Fatal("pull verb not found")
}

func TestWriteVerbLockOpensConfirmation(t *testing.T) {
	c := newLoadedContext(t)
	// Need a bundle with a ref for lock to apply. Use a direct selection.
	sel := Selection{
		Kind:  fleet.KindRevision,
		Key:   "test@1.0.0",
		Label: "test 1.0.0",
		Ref:   "/tmp/test",
		Local: true,
	}
	// Find the lock verb and run it.
	for _, v := range writeVerbs() {
		if v.Key == "L" {
			cmd := v.Run(c, sel)
			msg, ok := cmd().(pushMsg)
			if !ok {
				t.Fatalf("L produced %T, want pushMsg", cmd())
			}
			if msg.s.Title() != "Confirm" {
				t.Fatalf("L pushed %q, want Confirm", msg.s.Title())
			}
			return
		}
	}
	t.Fatal("lock verb not found")
}

func TestWriteVerbGenerateOpensConfirmation(t *testing.T) {
	c := newLoadedContext(t)
	// Need a local ref for generate to apply.
	sel := Selection{
		Kind:  fleet.KindRevision,
		Key:   "local@1.0.0",
		Label: "local 1.0.0",
		Ref:   "/tmp/local",
		Local: true,
	}
	// Find the generate verb and run it.
	for _, v := range writeVerbs() {
		if v.Key == "G" {
			cmd := v.Run(c, sel)
			msg, ok := cmd().(pushMsg)
			if !ok {
				t.Fatalf("G produced %T, want pushMsg", cmd())
			}
			if msg.s.Title() != "Confirm" {
				t.Fatalf("G pushed %q, want Confirm", msg.s.Title())
			}
			return
		}
	}
	t.Fatal("generate verb not found")
}

func TestHasRemoteRefWithRemoteRef(t *testing.T) {
	sel := Selection{Ref: "ghcr.io/acme/svc:1.0", Local: false}
	if !hasRemoteRef(sel) {
		t.Fatal("hasRemoteRef should return true for a remote ref")
	}
}

func TestHasRemoteRefWithLocalRef(t *testing.T) {
	sel := Selection{Ref: "/tmp/svc", Local: true}
	if hasRemoteRef(sel) {
		t.Fatal("hasRemoteRef should return false for a local ref")
	}
}

func TestHasRemoteRefWithEmptyRef(t *testing.T) {
	sel := Selection{Ref: "", Local: false}
	if hasRemoteRef(sel) {
		t.Fatal("hasRemoteRef should return false for empty ref")
	}
}

func TestHasLocalBundleWithLocalRef(t *testing.T) {
	sel := Selection{Ref: "/tmp/svc", Local: true}
	if !hasLocalBundle(sel) {
		t.Fatal("hasLocalBundle should return true for a local ref")
	}
}

func TestHasLocalBundleWithRemoteRef(t *testing.T) {
	sel := Selection{Ref: "ghcr.io/acme/svc:1.0", Local: false}
	if hasLocalBundle(sel) {
		t.Fatal("hasLocalBundle should return false for a remote ref")
	}
}

func TestHasLocalBundleWithEmptyRef(t *testing.T) {
	sel := Selection{Ref: "", Local: true}
	if hasLocalBundle(sel) {
		t.Fatal("hasLocalBundle should return false for empty ref")
	}
}
