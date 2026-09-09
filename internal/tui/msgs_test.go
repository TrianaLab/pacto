package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestSenderSendWithNilReceiver(t *testing.T) {
	var s *sender
	s.send(statusMsg{text: "test"}) // should not panic
}

func TestSenderSendWithNilProgram(t *testing.T) {
	s := &sender{}
	s.send(statusMsg{text: "test"}) // should not panic
}

func TestSenderSendWithProgram(t *testing.T) {
	m := New(testOptions())
	// Create a program that will exit immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithoutRenderer())
	s := &sender{p: p}

	// Start the program in the background
	go func() {
		_, _ = p.Run()
	}()

	// Give it a moment to start
	time.Sleep(10 * time.Millisecond)

	// Send should not panic and should not block indefinitely
	s.send(statusMsg{text: "test"})
}
