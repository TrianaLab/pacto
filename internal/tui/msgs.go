package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// pushMsg and popMsg drive the screen stack. They are messages rather than
// direct mutations so that a screen can request navigation from inside a
// tea.Cmd goroutine as easily as from Update.
type pushMsg struct{ s screen }
type popMsg struct{}

// errMsg carries a failure to the footer without unwinding the stack.
type errMsg struct{ err error }

// statusMsg sets the transient footer note.
type statusMsg struct{ text string }

// snapshotMsg delivers the assembled fleet snapshot, or the failure to build it.
type snapshotMsg struct {
	snap *fleet.FleetSnapshot
	err  error
}

// depResolvedMsg is posted from app-layer OnDepResolved callbacks, which fire
// from arbitrary goroutines. It carries no payload precisely so that it is safe
// to post from anywhere.
type depResolvedMsg struct{}

// sender posts messages into a running program from a goroutine. The program
// pointer is written exactly once, by Run, before p.Run() starts, and only ever
// read afterwards from tea.Cmd goroutines.
type sender struct{ p *tea.Program }

func (s *sender) send(m tea.Msg) {
	if s == nil || s.p == nil {
		return
	}
	s.p.Send(m)
}
