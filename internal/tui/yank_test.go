package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestYankProducesARunnableInvocation(t *testing.T) {
	c := newLoadedContext(t)
	sel := Selection{Kind: fleet.KindRevision, Key: "svc@1.0.0", Label: "svc 1.0.0", Ref: "./svc"}
	got := yankLine(c, sel)
	if !strings.HasPrefix(got, "pacto ") {
		t.Fatalf("yank = %q, want it to start with pacto", got)
	}
	if !strings.Contains(got, "./svc") {
		t.Fatalf("yank = %q, want it to name the selection", got)
	}
}

func TestYankForAnEntityWithNoBundleUsesFleetGet(t *testing.T) {
	c := newLoadedContext(t)
	sel := Selection{Kind: fleet.KindOwner, Key: "team:x", Label: "team:x"}
	if got := yankLine(c, sel); got != "pacto fleet get owner team:x" {
		t.Fatalf("yank = %q", got)
	}
}

func TestYankFallsBackToShowingTheLineWhenThereIsNoClipboard(t *testing.T) {
	orig := clipboardWrite
	t.Cleanup(func() { clipboardWrite = orig })
	clipboardWrite = nil

	c := newLoadedContext(t)
	l := newListScreen(c)
	cmd, handled := dispatchVerb(c, l, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !handled {
		t.Fatal("y was not handled")
	}
	msg, ok := cmd().(statusMsg)
	if !ok {
		t.Fatalf("got %T, want a statusMsg showing the line", cmd())
	}
	if !strings.Contains(msg.text, "pacto ") {
		t.Fatalf("status = %q, want the invocation", msg.text)
	}
}

func TestYankReportsAClipboardFailure(t *testing.T) {
	orig := clipboardWrite
	t.Cleanup(func() { clipboardWrite = orig })
	clipboardWrite = func(string) error { return errBoom }

	c := newLoadedContext(t)
	cmd, _ := dispatchVerb(c, newListScreen(c), tea.KeyPressMsg{Code: 'y', Text: "y"})
	if msg := cmd().(statusMsg); !strings.Contains(msg.text, "boom") {
		t.Fatalf("a clipboard failure was swallowed: %q", msg.text)
	}
}

func TestRefreshReloadsTheSnapshot(t *testing.T) {
	m := New(testOptions())
	cmd, handled := globalKey(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	if !handled || cmd == nil {
		t.Fatal("r did not trigger a reload")
	}
}

func TestYankArgvWithBundle(t *testing.T) {
	c := newLoadedContext(t)
	sel := Selection{Ref: "./svc", Label: "svc"}
	argv := yankArgv(c, sel)
	if len(argv) != 3 || argv[0] != "pacto" || argv[1] != "validate" || argv[2] != "./svc" {
		t.Fatalf("yankArgv = %v, want [pacto validate ./svc]", argv)
	}
}

func TestYankArgvWithoutBundle(t *testing.T) {
	c := newLoadedContext(t)
	sel := Selection{Kind: fleet.KindOwner, Key: "team:x", Label: "team:x"}
	argv := yankArgv(c, sel)
	if len(argv) != 5 || argv[0] != "pacto" || argv[1] != "fleet" || argv[2] != "get" || argv[3] != "owner" || argv[4] != "team:x" {
		t.Fatalf("yankArgv = %v, want [pacto fleet get owner team:x]", argv)
	}
}

func TestVerbYankWithClipboard(t *testing.T) {
	orig := clipboardWrite
	t.Cleanup(func() { clipboardWrite = orig })
	var copied string
	clipboardWrite = func(s string) error {
		copied = s
		return nil
	}

	c := newLoadedContext(t)
	sel := Selection{Ref: "./svc", Label: "svc"}
	cmd := verbYank(c, sel)
	msg := cmd().(statusMsg)
	if !strings.Contains(msg.text, "copied") {
		t.Fatalf("status = %q, want it to say copied", msg.text)
	}
	if copied != "pacto validate ./svc" {
		t.Fatalf("copied = %q, want pacto validate ./svc", copied)
	}
}

func TestReadOnlyRejectsAWriteEvenIfOneIsDispatched(t *testing.T) {
	// Defence in depth: verbList already hides write verbs in read-only mode,
	// but runWrite is the single choke point and must refuse independently.
	c := newLoadedContext(t)
	c.ReadOnly = true
	cmd := runWrite(c, "Push?", []string{"pacto", "push", "./svc"})
	if _, ok := cmd().(pushMsg); ok {
		t.Fatal("runWrite opened a confirmation in read-only mode")
	}
	if msg, ok := cmd().(statusMsg); !ok || !strings.Contains(msg.text, "read-only") {
		t.Fatalf("got %T, want a statusMsg naming read-only mode", cmd())
	}
}
