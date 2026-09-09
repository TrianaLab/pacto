package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// loadSnapshot assembles the fleet snapshot off the UI goroutine.
func loadSnapshot(c *Context) tea.Cmd {
	return func() tea.Msg {
		snap, err := c.Svc.Fleet(context.Background(), c.Fleet)
		if err != nil {
			return snapshotMsg{err: err}
		}
		_ = fleet.NewQuery(snap) // wired into the context in Task 7
		return snapshotMsg{snap: snap}
	}
}
