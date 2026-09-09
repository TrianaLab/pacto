package tui

import (
	tea "charm.land/bubbletea/v2"
)

// loadSnapshot assembles the fleet snapshot off the UI goroutine. It is the one
// slow operation in the session: every read afterwards is a pure query over the
// result.
func loadSnapshot(c *Context) tea.Cmd {
	return func() tea.Msg {
		snap, err := c.Svc.Fleet(c.Ctx, c.Fleet)
		if err != nil {
			return snapshotMsg{err: err}
		}
		return snapshotMsg{snap: snap}
	}
}
