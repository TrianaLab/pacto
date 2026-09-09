package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// newProgram is a seam so tests can run the model without a terminal. It is the
// constructor itself rather than a wrapper around it, because a wrapper body
// would be a statement no test can reach — every test replaces the seam.
var newProgram = tea.NewProgram

// Run starts the TUI and blocks until the user quits or ctx is cancelled.
func Run(ctx context.Context, o Options) error {
	m := New(o)
	opts := []tea.ProgramOption{tea.WithContext(ctx)}
	if o.Input != nil {
		opts = append(opts, tea.WithInput(o.Input))
	}
	if o.Output != nil {
		opts = append(opts, tea.WithOutput(o.Output))
	}
	p := newProgram(m, opts...)
	// Written once, before the program starts reading them from Cmd goroutines.
	m.ctx.Send.p = p
	m.ctx.Ctx = ctx
	_, err := p.Run()
	return err
}
