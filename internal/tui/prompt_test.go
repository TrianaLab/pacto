package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// openPrompt runs promptFor and returns the screen it pushed, so a test can
// drive the prompt without reaching into the constructor.
func openPrompt(t *testing.T, question string, onSubmit func(string) tea.Cmd) *promptScreen {
	t.Helper()
	batch, ok := promptFor(question, onSubmit)().(tea.BatchMsg)
	if !ok {
		t.Fatal("promptFor did not batch the push with the focus command")
	}
	msg, ok := batch[0]().(pushMsg)
	if !ok {
		t.Fatalf("the first batched command produced %T, want a pushMsg", batch[0]())
	}
	p, ok := msg.s.(*promptScreen)
	if !ok {
		t.Fatalf("promptFor pushed %T, want a *promptScreen", msg.s)
	}
	// The focus command exists and is harmless to run; running it here proves the
	// input was focused rather than left dead.
	batch[1]()
	if !p.input.Focused() {
		t.Fatal("the prompt opened with an unfocused input")
	}
	return p
}

func TestPromptCapturesTextSoGlobalKeysDoNotEatTyping(t *testing.T) {
	p := openPrompt(t, "Ref?", func(string) tea.Cmd { return nil })
	if !p.capturesText() {
		t.Fatal("the prompt does not claim printable keys, so q would pop mid-word")
	}
	if p.Title() != "Prompt" {
		t.Fatalf("Title = %q", p.Title())
	}
}

func TestPromptSubmitsTheAnswerAfterPoppingItself(t *testing.T) {
	var got string
	p := openPrompt(t, "Ref?", func(a string) tea.Cmd {
		got = a
		return status("submitted")
	})
	c := newLoadedContext(t)
	p.input.SetValue("  oci://ghcr.io/acme/svc:1.0.0  ")

	next, cmd := p.Update(c, tea.KeyPressMsg{Code: tea.KeyEnter})
	if next != screen(p) {
		t.Fatal("enter replaced the screen instead of returning the receiver")
	}
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	if got != "oci://ghcr.io/acme/svc:1.0.0" {
		t.Fatalf("onSubmit received %q, want the trimmed answer", got)
	}
	// The pop and whatever onSubmit pushes must not race. tea.Sequence's message
	// type is unexported, so the assertion available here is that it is not a
	// batch — which is exactly the change that would reintroduce the race.
	if _, batched := cmd().(tea.BatchMsg); batched {
		t.Fatal("the pop and the submission are batched; they must run in sequence")
	}
}

func TestPromptRefusesAnEmptyAnswer(t *testing.T) {
	called := false
	p := openPrompt(t, "Ref?", func(string) tea.Cmd {
		called = true
		return nil
	})
	c := newLoadedContext(t)
	p.input.SetValue("   ")

	_, cmd := p.Update(c, tea.KeyPressMsg{Code: tea.KeyEnter})
	msg, ok := cmd().(statusMsg)
	if !ok {
		t.Fatalf("an empty answer produced %T, want a statusMsg", cmd())
	}
	if !strings.Contains(msg.text, "esc") {
		t.Fatalf("status = %q, want it to say how to get out", msg.text)
	}
	if called {
		t.Fatal("an empty answer was submitted")
	}
}

func TestPromptEscapeCancels(t *testing.T) {
	called := false
	p := openPrompt(t, "Ref?", func(string) tea.Cmd {
		called = true
		return nil
	})
	c := newLoadedContext(t)
	_, cmd := p.Update(c, tea.KeyPressMsg{Code: tea.KeyEscape})
	if _, ok := cmd().(popMsg); !ok {
		t.Fatalf("esc produced %T, want a popMsg", cmd())
	}
	if called {
		t.Fatal("cancelling still submitted")
	}
}

func TestPromptTypingReachesTheInput(t *testing.T) {
	p := openPrompt(t, "Plugin?", func(string) tea.Cmd { return nil })
	c := newLoadedContext(t)
	for _, r := range "schema-infer" {
		p.Update(c, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if p.input.Value() != "schema-infer" {
		t.Fatalf("input value = %q, want the typed text", p.input.Value())
	}
}

func TestPromptViewShowsTheQuestionAndTheWayOut(t *testing.T) {
	p := openPrompt(t, "Registry reference?", func(string) tea.Cmd { return nil })
	c := newLoadedContext(t)
	// A non-key message must reach the input rather than being dropped.
	p.Update(c, tea.WindowSizeMsg{Width: 100, Height: 30})
	view := p.View(c)
	for _, want := range []string{"Registry reference?", "enter", "esc"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the prompt view does not mention %q:\n%s", want, view)
		}
	}
}
