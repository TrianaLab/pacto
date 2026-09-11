package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// newProgram is a seam so tests can run the model without a terminal. It is the
// constructor itself rather than a wrapper around it, because a wrapper body
// would be a statement no test can reach — every test replaces the seam. It is
// also the ONLY seam: Options once carried an Input and an Output that internal/cli
// never set, so the two branches reading them were reachable from tests alone,
// and a test that wants a headless program already replaces this.
var newProgram = tea.NewProgram

// Run starts the TUI and blocks until the user quits or ctx is cancelled.
func Run(ctx context.Context, o Options) error {
	m := New(o)
	p := newProgram(m, tea.WithContext(ctx))
	// Written once, before the program starts reading them from Cmd goroutines.
	m.ctx.Send.p = p
	m.ctx.Ctx = ctx
	_, err := p.Run()
	return err
}
