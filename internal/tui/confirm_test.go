package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestConfirmShowsTheExactCommand(t *testing.T) {
	c := newLoadedContext(t)
	s := newConfirmScreen("Push this bundle?", []string{"pacto", "push", "./svc", "oci://r/s:1"}, nil)
	out := s.View(c)
	for _, want := range []string{"Push this bundle?", "pacto push ./svc oci://r/s:1", "y", "n"} {
		if !strings.Contains(out, want) {
			t.Errorf("confirm screen omits %q:\n%s", want, out)
		}
	}
}

func TestConfirmYesRunsTheAction(t *testing.T) {
	c := newLoadedContext(t)
	ran := false
	s := newConfirmScreen("go?", []string{"pacto", "push"}, func() tea.Cmd {
		ran = true
		return nil
	})
	if _, cmd := s.Update(c, tea.KeyPressMsg{Code: 'y', Text: "y"}); cmd == nil && !ran {
		t.Fatal("y neither ran the action nor produced a command")
	}
	if !ran {
		t.Fatal("y did not run the action")
	}
}

// TestConfirmYesPopsBeforeItRuns pins A19. Batched, the pop and the action
// race, so an onYes that pushes an output screen can have it thrown away by the
// pop that was meant to precede it — the same shape as the bug prompt.go
// already carries a comment about.
func TestConfirmYesPopsBeforeItRuns(t *testing.T) {
	c := newLoadedContext(t)
	s := newConfirmScreen("go?", []string{"pacto", "push"}, func() tea.Cmd {
		return push(loadingScreen{})
	})
	_, cmd := s.Update(c, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if _, batched := cmd().(tea.BatchMsg); batched {
		t.Fatal("y batched the pop with the action; they must be sequenced")
	}
	members := cmdMembers(t, cmd)
	if len(members) != 2 {
		t.Fatalf("y produced %d commands, want the pop and the action", len(members))
	}
	if _, ok := members[0]().(popMsg); !ok {
		t.Fatalf("the first command is %T, want the pop to run first", members[0]())
	}
}

func TestConfirmNoPopsWithoutRunning(t *testing.T) {
	c := newLoadedContext(t)
	ran := false
	s := newConfirmScreen("go?", []string{"pacto", "push"}, func() tea.Cmd { ran = true; return nil })
	_, cmd := s.Update(c, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if ran {
		t.Fatal("n ran the action")
	}
	if _, ok := cmd().(popMsg); !ok {
		t.Fatalf("n produced %T, want popMsg", cmd())
	}
}

func TestConfirmIgnoresEveryOtherKey(t *testing.T) {
	c := newLoadedContext(t)
	ran := false
	s := newConfirmScreen("go?", nil, func() tea.Cmd { ran = true; return nil })
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter}, {Code: ' ', Text: " "}, {Code: 'Y', Text: "Y"},
	} {
		if _, cmd := s.Update(c, k); cmd != nil {
			t.Errorf("%s produced a command; only y and n may act", k.String())
		}
	}
	if ran {
		t.Fatal("a key other than y ran the action")
	}
}

func TestConfirmWithNoActionStillPops(t *testing.T) {
	// A nil onYes must not panic — it is what a mis-wired verb produces.
	c := newLoadedContext(t)
	s := newConfirmScreen("go?", []string{"pacto", "test"}, nil)
	_, cmd := s.Update(c, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("y with nil action produced no command, want a popMsg")
	}
	if _, ok := cmd().(popMsg); !ok {
		t.Fatalf("y with nil action produced %T, want popMsg", cmd())
	}
}

func TestConfirmTitle(t *testing.T) {
	s := newConfirmScreen("test?", nil, nil)
	if got := s.Title(); got != "Confirm" {
		t.Fatalf("Title() = %q, want %q", got, "Confirm")
	}
}

func TestConfirmIgnoresNonKeyPressMessages(t *testing.T) {
	c := newLoadedContext(t)
	s := newConfirmScreen("test?", nil, nil)
	next, cmd := s.Update(c, tea.WindowSizeMsg{Width: 100, Height: 50})
	if next != s {
		t.Fatal("non-KeyPressMsg changed the screen")
	}
	if cmd != nil {
		t.Fatal("non-KeyPressMsg produced a command")
	}
}

// forgedName is a service name that repaints the line it is printed on: the
// carriage return returns the cursor to column zero and the erase-line wipes
// what was already there, so everything before it on that line is replaced by
// what follows. bubbletea's cell renderer acts on both while it composes the
// frame, so a terminal that filters escapes is not a defence.
const forgedName = "my-service\r\x1b[2Kpacto lock --check ./my-service"

func TestConfirmCannotBeForged(t *testing.T) {
	c := newLoadedContext(t)
	s := newConfirmScreen("Rewrite the lock file for "+forgedName+"?",
		[]string{"pacto", "lock", "--update", "./" + forgedName}, nil)
	out := s.View(c)

	if strings.Contains(out, "\r") || strings.Contains(out, "\x1b[2K") {
		t.Fatalf("the confirmation emits the bytes that repaint the line, so the gate can show a command other than the one y runs:\n%q", out)
	}
	// The gate is only useful if it still says what will run.
	if !strings.Contains(out, "lock --update") {
		t.Fatalf("the confirmation no longer shows the command it will run:\n%q", out)
	}
}
