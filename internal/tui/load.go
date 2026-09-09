package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// loadSnapshot assembles the fleet snapshot off the UI goroutine. It is the one
// slow operation in the session: every read afterwards is a pure query over the
// result.
func loadSnapshot(c *Context) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		if testCtx != nil {
			ctx = testCtx()
		}
		snap, err := c.Svc.Fleet(ctx, c.Fleet)
		if err != nil {
			return snapshotMsg{err: err}
		}
		return snapshotMsg{snap: snap}
	}
}

// testCtx is a test seam for injecting a context.
var testCtx func() context.Context
