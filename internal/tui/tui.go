// Package tui is the full-screen terminal front-end over the pacto CLI. It
// loads one fleet snapshot, lets you navigate it and runs the CLI's verbs
// against whatever is selected — reads in-process against the snapshot, writes
// by shelling out to the real pacto binary.
package tui

import (
	"context"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// Options configures a TUI session.
type Options struct {
	Svc   *app.Service
	Fleet app.FleetOptions
	// SourceArgs is the source flags the session was launched with, already in
	// argv form. Fleet holds the same information but has transformed it, so a
	// yanked line is rendered from this rather than reversed out of that.
	SourceArgs []string
	ReadOnly   bool   // suppress every write verb
	Exe        string // absolute path to the pacto binary used for write verbs
	Input      io.Reader
	Output     io.Writer
}

// Context is the state every screen shares. Screens read it and never replace
// it; the root Model owns the pointer.
//
// The event loop OWNS this struct. Send, Ctx, Svc and Exe are written once
// before the program starts and are safe to read from anywhere; every other
// field is event-loop-only, and Query, Snapshot and Status are reassigned on
// every reload. So a tea.Cmd closure must read what it needs into a local
// BEFORE it returns and close over the local — reading a field from inside the
// goroutine races the next snapshotMsg. There is deliberately no mutex: one
// would make every future field read look safe when the invariant is ownership,
// not locking.
type Context struct {
	Ctx        context.Context // session context that cancels in-flight loads
	Svc        *app.Service
	Fleet      app.FleetOptions
	SourceArgs []string // the source flags this session was launched with
	Query      *fleet.Query
	Snapshot   *fleet.FleetSnapshot
	Send       *sender
	Exe        string
	ReadOnly   bool
	Width      int
	Height     int
	Status     string
	// pendingDiff and pendingImpact hold the left-hand side of a two-selection
	// verb between the two keypresses that make it up.
	pendingDiff   Selection
	pendingImpact Selection
}

// Model is the root tea.Model: a screen stack plus the shared context.
type Model struct {
	ctx   *Context
	stack []screen
	err   error
}

// New builds the root model with a loading screen on top.
func New(o Options) *Model {
	return &Model{
		ctx: &Context{
			Ctx:        context.Background(),
			Svc:        o.Svc,
			Fleet:      o.Fleet,
			SourceArgs: o.SourceArgs,
			Send:       &sender{},
			Exe:        o.Exe,
			ReadOnly:   o.ReadOnly,
			Width:      80,
			Height:     24,
		},
		stack: []screen{loadingScreen{}},
	}
}

// top returns the screen that currently owns the keyboard.
func (m *Model) top() screen { return m.stack[len(m.stack)-1] }

// Init kicks off the snapshot load.
func (m *Model) Init() tea.Cmd { return loadSnapshot(m.ctx) }

// Update routes global keys and stack navigation, then delegates to the top
// screen. Global keys are handled first and are not forwarded.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.ctx.Width, m.ctx.Height = msg.Width, msg.Height
	case snapshotMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.ctx.Query = fleet.NewQuery(msg.snap)
		m.ctx.Snapshot = msg.snap
		// The snapshot is the world every screen was rendered from, so a fresh
		// one clears both the progress note and any error left over from the
		// write that asked for it.
		m.err = nil
		m.ctx.Status = ""
		if _, starting := m.top().(loadingScreen); starting {
			// Replace rather than push: the loading screen is not somewhere the
			// user can go back to.
			m.stack[len(m.stack)-1] = newListScreen(m.ctx)
			return m, nil
		}
		m.reloadScreens()
		return m, nil
	case pushMsg:
		m.stack = append(m.stack, msg.s)
		return m, nil
	case popMsg:
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
		}
		return m, nil
	case errMsg:
		m.err = msg.err
		return m, nil
	case statusMsg:
		m.ctx.Status = msg.text
		return m, nil
	case execDoneMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// The write changed the world the snapshot describes, so reload it
		// rather than leaving a confidently stale list on screen.
		m.err = nil
		m.ctx.Status = "reloading the snapshot"
		return m, loadSnapshot(m.ctx)
	case tea.KeyPressMsg:
		if cmd, handled := globalKey(m, msg); handled {
			return m, cmd
		}
	}
	next, cmd := m.top().Update(m.ctx, msg)
	m.stack[len(m.stack)-1] = next
	return m, cmd
}

// reloadScreens re-queries every screen on the stack against the new snapshot.
// r and a finished write both reload from arbitrary depth, so the stack has to
// survive it: replacing the top with a fresh list turned a service detail into a
// second list, with a breadcrumb reading "Fleet > Fleet" and a back key that
// went from a list to a list. Screens below the top are refreshed too, because
// the reader will pop down to them and a stale one is no more honest for being
// out of sight.
func (m *Model) reloadScreens() {
	for _, s := range m.stack {
		if r, ok := s.(interface{ refresh(*Context) }); ok {
			r.refresh(m.ctx)
		}
	}
}

// View composes header, body and footer, then asks for the alt screen on every
// frame — bubbletea v2 has no WithAltScreen program option, the request lives
// on the View and is re-read each render.
func (m *Model) View() tea.View {
	body := m.top().View(m.ctx)
	v := tea.NewView(m.header() + "\n" + body + "\n" + m.footer())
	v.AltScreen = true
	return v
}

// header renders the breadcrumb trail. Sanitising the crumbs here rather than
// in each Title covers every screen at once, including the output screens whose
// titles are built from a selection's label.
func (m *Model) header() string {
	crumbs := make([]string, 0, len(m.stack))
	for _, s := range m.stack {
		crumbs = append(crumbs, safeText(s.Title()))
	}
	title := headerStyle.Render("pacto  " + strings.Join(crumbs, " > "))
	if m.ctx.ReadOnly {
		title += "  " + warnStyle.Render("[read-only]")
	}
	return title
}

func (m *Model) footer() string {
	// Both of these carry fleet content: a status is built from a selection's
	// label, and an error quotes the path or the name that failed.
	if m.err != nil {
		return errorStyle.Render("error: " + safeText(m.err.Error()))
	}
	if m.ctx.Status != "" {
		return dimStyle.Render(safeText(m.ctx.Status))
	}
	return dimStyle.Render("?: help   q: back   ctrl+c: quit")
}
