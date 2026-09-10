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
		return tea.NewProgram(m, append(opts, tea.WithoutRenderer(), tea.WithFilter(filter))...)
	}

	// A deadline rather than a cancelled context: the program has to live long
	// enough to take a message, or there is nothing to observe. The reader and
	// the writer are what keep it off /dev/tty, which go test cannot open.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	o := testOptions()
	o.Input, o.Output = strings.NewReader(""), io.Discard
	if err := Run(ctx, o); err != nil {
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

func TestRunWithInputAndOutput(t *testing.T) {
	orig := newProgram
	t.Cleanup(func() { newProgram = orig })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var receivedOpts []tea.ProgramOption
	newProgram = func(m tea.Model, opts ...tea.ProgramOption) *tea.Program {
		receivedOpts = opts
		return tea.NewProgram(m, append(opts, tea.WithoutRenderer())...)
	}

	opts := testOptions()
	opts.Input = &fakeReader{}
	opts.Output = &fakeWriter{}

	_ = Run(ctx, opts)

	if len(receivedOpts) < 3 {
		t.Fatalf("expected at least 3 options (ctx, input, output), got %d", len(receivedOpts))
	}
}

type fakeReader struct{}

func (f *fakeReader) Read(p []byte) (n int, err error) { return 0, nil }

type fakeWriter struct{}

func (f *fakeWriter) Write(p []byte) (n int, err error) { return len(p), nil }
