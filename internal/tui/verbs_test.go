package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestVerbKeysAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, v := range verbList(newLoadedContext(t)) {
		if prev, dup := seen[v.Key]; dup {
			t.Errorf("key %q is bound to both %q and %q", v.Key, prev, v.Help)
		}
		seen[v.Key] = v.Help
	}
	for _, g := range globalBindings() {
		if help, clash := seen[g.Key]; clash {
			t.Errorf("key %q is both a global (%q) and a verb (%q)", g.Key, g.Help, help)
		}
	}
}

func TestReadOnlyHidesEveryWriteVerb(t *testing.T) {
	c := newLoadedContext(t)
	c.ReadOnly = true
	for _, v := range verbList(c) {
		if v.Write {
			t.Errorf("write verb %q is offered in read-only mode", v.Key)
		}
	}
}

func TestEveryVerbHasHelpAndAnArgv(t *testing.T) {
	c := newLoadedContext(t)
	sel := Selection{Kind: fleet.KindRevision, Key: "k", Label: "l", Ref: "./svc"}
	for _, v := range verbList(c) {
		if v.Help == "" || v.Run == nil || v.Argv == nil || v.Applies == nil {
			t.Errorf("verb %q is incomplete: %+v", v.Key, v)
			continue
		}
		if argv := v.Argv(c, sel); len(argv) == 0 || argv[0] != "pacto" {
			t.Errorf("verb %q produced argv %v; it must start with pacto so y can yank it", v.Key, argv)
		}
	}
}

func TestBundleVerbsDoNotApplyToAnOwner(t *testing.T) {
	c := newLoadedContext(t)
	owner := Selection{Kind: fleet.KindOwner, Key: "team:x"}
	for _, v := range verbList(c) {
		if v.Key == "v" || v.Key == "E" || v.Key == "l" {
			if v.Applies(owner) {
				t.Errorf("verb %q claims to apply to an owner, which has no bundle", v.Key)
			}
		}
	}
}

func TestDispatchIgnoresAnUnboundKey(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	if _, handled := dispatchVerb(c, s, tea.KeyPressMsg{Code: 'Z', Text: "Z"}); handled {
		t.Fatal("an unbound key was consumed")
	}
}

func TestDispatchWithNothingSelectedSetsAStatusInsteadOfRunning(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	l.entities = nil
	l.tbl.SetRows(nil)
	cmd, handled := dispatchVerb(c, l, tea.KeyPressMsg{Code: 'v', Text: "v"})
	if !handled {
		t.Fatal("v was not handled")
	}
	msg, ok := cmd().(statusMsg)
	if !ok {
		t.Fatalf("got %T, want statusMsg", cmd())
	}
	if msg.text == "" {
		t.Fatal("the status message is empty")
	}
}

func TestDiffNeedsTwoSelections(t *testing.T) {
	c := newLoadedContext(t)
	// Get a service with a bundle ref explicitly
	ref := firstEntityOfKind(t, c, fleet.KindRevision)
	sel, err := resolveSelection(c, ref)
	if err != nil {
		t.Fatalf("resolveSelection: %v", err)
	}
	if sel.Ref == "" {
		t.Fatal("the fixture revision resolved to no bundle ref, so this test would verify nothing")
	}

	l := newListScreen(c).(*listScreen)
	// Find the revision in the list and set the cursor on it
	found := false
	for i, e := range l.entities {
		if e.Kind == fleet.KindRevision && e.Key == ref.Key {
			l.tbl.SetCursor(i)
			found = true
			break
		}
	}
	if !found {
		t.Fatal("the revision is not in the list")
	}

	// The first d arms the comparison and says so.
	cmd, _ := dispatchVerb(c, l, tea.KeyPressMsg{Code: 'd', Text: "d"})
	if _, ok := cmd().(statusMsg); !ok {
		t.Fatalf("the first d produced %T, want a statusMsg arming the diff", cmd())
	}
	if c.pendingDiff.Ref == "" {
		t.Fatal("the first d did not record the left-hand side")
	}
	// The second d runs it and disarms.
	cmd, _ = dispatchVerb(c, l, tea.KeyPressMsg{Code: 'd', Text: "d"})
	if cmd == nil {
		t.Fatal("the second d produced no command")
	}
	if c.pendingDiff.Ref != "" {
		t.Fatal("the pending diff was not cleared after running")
	}
}

func TestRunReadOpensAnOutputScreen(t *testing.T) {
	c := newLoadedContext(t)
	ran := make(chan struct{})
	cmd := runRead(c, "test", func(o *outputScreen) error {
		emit(c, o, "hello\nworld")
		close(ran)
		return nil
	})
	// runRead batches three commands and push is first, so the loop finds the
	// screen without also firing the spinner tick, which sleeps.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("runRead produced %T, want a tea.BatchMsg", cmd())
	}
	m, ok := batch[0]().(pushMsg)
	if !ok {
		t.Fatalf("the first batched command produced %T, want a pushMsg", batch[0]())
	}
	if _, isOutput := m.s.(*outputScreen); !isOutput {
		t.Fatalf("runRead pushed %T, want an *outputScreen", m.s)
	}
	batch[len(batch)-1]() // the work command, last in the batch
	<-ran
}

func TestScreenLocalKeysDoNotShadowVerbs(t *testing.T) {
	c := newLoadedContext(t)
	verbs := map[string]bool{}
	for _, v := range verbList(c) {
		verbs[v.Key] = true
	}
	// Every key a screen handles itself, gathered by reading the switch
	// statements. Keep this list in step with them.
	local := map[string][]string{
		"list":      {"/", "a", "tab", "shift+tab", "enter", "esc"},
		"attention": {"tab", "shift+tab", "enter"},
		"graph":     {"tab", "+", "=", "-", "_"},
	}
	for screen, keys := range local {
		for _, k := range keys {
			if verbs[k] {
				t.Errorf("%s handles %q locally, but it is also a verb key", screen, k)
			}
		}
	}
}

func TestVerbCommandsOnAZeroContext(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("VerbCommands panicked on a zero Context: %v", r)
		}
	}()
	cmds := VerbCommands()
	if len(cmds) == 0 {
		t.Fatal("VerbCommands returned an empty list, verb table is broken")
	}
}

func TestCommandPathDefensiveChecks(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"empty", []string{}, ""},
		{"too short", []string{"pacto"}, ""},
		{"wrong prefix", []string{"not-pacto", "validate"}, ""},
		{"simple command", []string{"pacto", "validate"}, "validate"},
		{"fleet subcommand", []string{"pacto", "fleet", "get"}, "fleet get"},
		{"non-subcommand", []string{"pacto", "validate", "ref"}, "validate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commandPath(tt.argv)
			if got != tt.want {
				t.Errorf("commandPath(%v) = %q, want %q", tt.argv, got, tt.want)
			}
		})
	}
}
