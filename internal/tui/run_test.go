package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestRunWiresTheSenderBeforeStarting(t *testing.T) {
	var capturedModel *Model
	orig := newProgram
	t.Cleanup(func() { newProgram = orig })

	newProgram = func(m tea.Model, opts ...tea.ProgramOption) *tea.Program {
		capturedModel = m.(*Model)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return tea.NewProgram(m, append(opts, tea.WithContext(ctx), tea.WithoutRenderer())...)
	}

	_ = Run(context.Background(), testOptions())

	if capturedModel == nil {
		t.Fatal("newProgram was not called")
	}
	if capturedModel.ctx.Send.p == nil {
		t.Fatal("ctx.Send.p is nil; Run must set it before p.Run()")
	}
}

func TestRunReturnsPPromptlyWhenContextIsCancelled(t *testing.T) {
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
