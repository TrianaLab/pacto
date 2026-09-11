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

// statusMsg sets the transient footer note.
type statusMsg struct{ text string }

// snapshotMsg delivers the assembled fleet snapshot, or the failure to build it.
type snapshotMsg struct {
	snap *fleet.FleetSnapshot
	err  error
}

// depResolvedMsg reports that one more dependency finished resolving. It is
// posted from app-layer OnDepResolved callbacks, which fire from arbitrary
// goroutines, so it carries nothing but the id of the run that owns it — no
// pointers, nothing that needs a lock.
type depResolvedMsg struct{ id int }

// msgSink is what a sender posts to. *tea.Program is the real one; tests pass a
// recorder, because a verb's whole observable output is the messages it sends.
type msgSink interface{ Send(tea.Msg) }

// sender posts messages into a running program from a goroutine. The program
// pointer is written exactly once, by Run, before p.Run() starts, and only ever
// read afterwards from tea.Cmd goroutines.
type sender struct{ p msgSink }

func (s *sender) send(m tea.Msg) {
	if s == nil || s.p == nil {
		return
	}
	s.p.Send(m)
}
