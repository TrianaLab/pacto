package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// Run starts the TUI and blocks until the user quits or ctx is cancelled.
func Run(ctx context.Context, o Options) error { return run(ctx, o, tea.NewProgram) }

// run is Run with the program constructor as a parameter, so tests can run the
// model without a terminal. Options once carried an Input and an Output that
// internal/cli never set, so the two branches reading them were reachable from
// tests alone; a test that wants a headless program passes its own newProgram.
func run(ctx context.Context, o Options, newProgram func(tea.Model, ...tea.ProgramOption) *tea.Program) error {
	m := New(o)
	p := newProgram(m, tea.WithContext(ctx))
	// Written once, before the program starts reading them from Cmd goroutines.
	m.ctx.Send.p = p
	m.ctx.Ctx = ctx
	_, err := p.Run()
	return err
}
