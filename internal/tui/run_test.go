package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestRunWiresTheSenderBeforeStarting observes from INSIDE p.Run(). Reading
// ctx.Send.p after Run has returned says only that it was wired, which is true
// whether the assignment happens before p.Run() or after it — and after it,
// every outputLineMsg a tea.Cmd goroutine posts in the meantime is swallowed by
// the nil guard in sender.send, and the assignment itself races the loop
// reading the field. tea.WithFilter runs on the event loop ahead of each
// message, so the first one is the earliest moment the program offers.
func TestRunWiresTheSenderBeforeStarting(t *testing.T) {
	var capturedModel *Model
	var sawAMessage, wiredAtFirstMessage bool
	orig := newProgram
	t.Cleanup(func() { newProgram = orig })

	newProgram = func(m tea.Model, opts ...tea.ProgramOption) *tea.Program {
		capturedModel = m.(*Model)
		filter := func(_ tea.Model, _ tea.Msg) tea.Msg {
			if !sawAMessage {
				sawAMessage = true
				wiredAtFirstMessage = capturedModel.ctx.Send.p != nil
			}
			// The observation is made; a program with no terminal has nothing
			// else to do, and quitting here is what bounds the test.
			return tea.QuitMsg{}
		}
		// The reader and the writer are what keep the program off /dev/tty, which
		// go test cannot open. They are supplied here rather than through Options
		// because nothing in internal/cli ever set them: a field only a test writes
		// is a seam, and the seam this package already has is newProgram.
		return tea.NewProgram(m, append(opts,
			tea.WithInput(strings.NewReader("")),
			tea.WithOutput(io.Discard),
			tea.WithoutRenderer(),
			tea.WithFilter(filter))...)
	}

	// A deadline rather than a cancelled context: the program has to live long
	// enough to take a message, or there is nothing to observe.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := Run(ctx, testOptions()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if capturedModel == nil {
		t.Fatal("newProgram was not called")
	}
	if !sawAMessage {
		t.Fatal("the event loop processed no message, so the ordering was never observed")
	}
	if !wiredAtFirstMessage {
		t.Fatal("ctx.Send.p was still nil when the event loop took its first message; Run must set it before p.Run()")
	}
}

func TestRunReturnsPromptlyWhenContextIsCancelled(t *testing.T) {
	orig := newProgram
	t.Cleanup(func() { newProgram = orig })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	newProgram = func(m tea.Model, opts ...tea.ProgramOption) *tea.Program {
		return tea.NewProgram(m, append(opts, tea.WithoutRenderer())...)
	}

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, testOptions())
	}()

	select {
	case <-done:
		// Run returned promptly
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2 seconds after context cancellation")
	}
}
