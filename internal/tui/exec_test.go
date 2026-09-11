package tui

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRunWriteAsksBeforeItRuns(t *testing.T) {
	c := newLoadedContext(t)
	cmd := runWrite(c, "Push?", []string{"pacto", "push", "./svc", "oci://r/s:1"})
	msg, ok := cmd().(pushMsg)
	if !ok {
		t.Fatalf("runWrite produced %T, want a pushMsg opening the confirmation", cmd())
	}
	if msg.s.Title() != "Confirm" {
		t.Fatalf("runWrite pushed %q, want the confirmation screen", msg.s.Title())
	}
}

func TestConfirmingAWriteExecsTheRealBinary(t *testing.T) {
	var got []string
	orig := execProcess
	t.Cleanup(func() { execProcess = orig })
	execProcess = func(cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		got = cmd.Args
		return func() tea.Msg { return fn(nil) }
	}

	c := newLoadedContext(t)
	c.Exe = "/opt/bin/pacto"
	conf := runWrite(c, "Push?", []string{"pacto", "push", "./svc"})().(pushMsg).s
	// confirmScreen calls onYes during Update, so execProcess has already been
	// reached by the time Update returns; the returned sequence only carries it.
	_, cmd := conf.Update(c, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("confirming produced no command")
	}
	// Run the sequence so the exec callback fires.
	for _, sub := range cmdMembers(t, cmd) {
		if _, ok := sub().(execDoneMsg); ok {
			break
		}
	}
	if len(got) < 2 || got[0] != "/opt/bin/pacto" || got[1] != "push" {
		t.Fatalf("exec argv = %v, want the real binary path followed by the subcommand", got)
	}
	for _, a := range got {
		if a == "pacto" {
			t.Fatal("the literal word pacto leaked into argv; argv[0] must be c.Exe")
		}
	}
}

func TestDecliningAWriteExecsNothing(t *testing.T) {
	called := false
	orig := execProcess
	t.Cleanup(func() { execProcess = orig })
	execProcess = func(*exec.Cmd, tea.ExecCallback) tea.Cmd {
		called = true
		return nil
	}
	c := newLoadedContext(t)
	conf := runWrite(c, "Push?", []string{"pacto", "push", "./svc"})().(pushMsg).s
	conf.Update(c, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if called {
		t.Fatal("declining still executed the command")
	}
}

func TestExecFailureIsReported(t *testing.T) {
	m := New(testOptions())
	next, _ := m.Update(execDoneMsg{err: errBoom})
	if !strings.Contains(next.View().Content, "boom") {
		t.Fatalf("a failed subprocess is not reported:\n%s", next.View().Content)
	}
}

func TestExecSuccessRefreshesTheSnapshot(t *testing.T) {
	// A write changed something on disk or in a registry, so the snapshot on
	// screen is now stale. A successful exec must reload it rather than leave a
	// confidently wrong list up.
	m := New(testOptions())
	next, cmd := m.Update(execDoneMsg{})
	if cmd == nil {
		t.Fatal("a successful write did not trigger a reload")
	}
	if _, ok := cmd().(snapshotMsg); !ok {
		t.Fatalf("the reload produced %T, want a snapshotMsg", cmd())
	}
	if next.(*Model).err != nil {
		t.Fatal("a successful write left an error on the model")
	}
}

func TestWriteVerbsCarryTheWriteFlag(t *testing.T) {
	vs := writeVerbs()
	// Asserted so the loop below cannot pass vacuously if the table empties.
	if len(vs) != 4 {
		t.Fatalf("writeVerbs returned %d verbs, want the four documented ones", len(vs))
	}
	for _, v := range vs {
		if !v.Write {
			t.Errorf("verb %q is in writeVerbs but is not marked Write", v.Key)
		}
	}
}

// TestTheWriteSubprocessIsNotToldToAdvertise pins A8. The child is a second
// pacto, so without these it runs the update check root.go:96 does on every
// invocation and can print an upgrade banner into the middle of a push.
func TestTheWriteSubprocessIsNotToldToAdvertise(t *testing.T) {
	var got *exec.Cmd
	orig := execProcess
	t.Cleanup(func() { execProcess = orig })
	execProcess = func(cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		got = cmd
		return func() tea.Msg { return fn(nil) }
	}

	c := newLoadedContext(t)
	execVerb(c, []string{"push", "./svc"})
	for _, want := range []string{"PACTO_NO_UPDATE_CHECK=1", "NO_COLOR=1"} {
		if !slices.Contains(got.Env, want) {
			t.Errorf("the write subprocess env is missing %s", want)
		}
	}
	// The rest of the environment still has to reach it: a registry credential
	// or a KUBECONFIG lives there, and a push without them fails.
	if len(got.Env) <= 2 {
		t.Fatalf("env = %v, want the inherited environment plus the suppressions", got.Env)
	}
}
