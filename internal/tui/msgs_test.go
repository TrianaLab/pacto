package tui

import (
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
	received := make(chan string, 1)
	ready := make(chan struct{})
	tm := &testModel{msgCh: received, readyCh: ready}

	in, out := &fakeInput{}, &fakeOutput{}
	p := tea.NewProgram(tm, tea.WithInput(in), tea.WithOutput(out), tea.WithoutRenderer())
	s := &sender{p: p}

	go func() {
		_, _ = p.Run()
	}()

	t.Cleanup(func() {
		p.Send(tea.Quit())
	})

	// Wait for the program to start processing messages
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("program did not start within 2 seconds")
	}

	s.send(testMsg{payload: "test payload"})

	select {
	case payload := <-received:
		if payload != "test payload" {
			t.Fatalf("received payload %q, want %q", payload, "test payload")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("message did not reach the model within 2 seconds")
	}
}

type fakeInput struct{}

func (f *fakeInput) Read(p []byte) (n int, err error) {
	time.Sleep(time.Hour)
	return 0, nil
}

type fakeOutput struct{}

func (f *fakeOutput) Write(p []byte) (n int, err error) {
	return len(p), nil
}

// testMsg is a custom message type for testing sender.
type testMsg struct {
	payload string
}

// testModel is a minimal tea.Model for testing sender.
type testModel struct {
	msgCh   chan string
	readyCh chan struct{}
	started bool
}

func (tm *testModel) Init() tea.Cmd { return nil }

func (tm *testModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !tm.started && tm.readyCh != nil {
		tm.started = true
		close(tm.readyCh)
	}
	if m, ok := msg.(testMsg); ok && tm.msgCh != nil {
		select {
		case tm.msgCh <- m.payload:
		default:
		}
	}
	return tm, nil
}

func (tm *testModel) View() tea.View {
	return tea.NewView("")
}
