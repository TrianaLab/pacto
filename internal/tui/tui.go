// Package tui is the full-screen terminal front-end over the pacto CLI. It
// loads one fleet snapshot, lets you navigate it and runs the CLI's verbs
// against whatever is selected — reads in-process against the snapshot, writes
// by shelling out to the real pacto binary.
package tui

import (
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// Options configures a TUI session.
type Options struct {
	Svc      *app.Service
	Fleet    app.FleetOptions
	ReadOnly bool   // suppress every write verb
	Exe      string // absolute path to the pacto binary used for write verbs
	Input    io.Reader
	Output   io.Writer
}

// Context is the state every screen shares. Screens read it and never replace
// it; the root Model owns the pointer.
type Context struct {
	Svc      *app.Service
	Fleet    app.FleetOptions
	Query    *fleet.Query
	Send     *sender
	Exe      string
	ReadOnly bool
	Width    int
	Height   int
	Status   string
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
			Svc:      o.Svc,
			Fleet:    o.Fleet,
			Send:     &sender{},
			Exe:      o.Exe,
			ReadOnly: o.ReadOnly,
			Width:    80,
			Height:   24,
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
	case tea.KeyPressMsg:
		if cmd, handled := globalKey(m, msg); handled {
			return m, cmd
		}
	}
	next, cmd := m.top().Update(m.ctx, msg)
	m.stack[len(m.stack)-1] = next
	return m, cmd
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

func (m *Model) header() string {
	crumbs := make([]string, 0, len(m.stack))
	for _, s := range m.stack {
		crumbs = append(crumbs, s.Title())
	}
	title := headerStyle.Render("pacto  " + strings.Join(crumbs, " > "))
	if m.ctx.ReadOnly {
		title += "  " + warnStyle.Render("[read-only]")
	}
	return title
}

func (m *Model) footer() string {
	if m.err != nil {
		return errorStyle.Render("error: " + m.err.Error())
	}
	if m.ctx.Status != "" {
		return dimStyle.Render(m.ctx.Status)
	}
	return dimStyle.Render("?: help   q: back   ctrl+c: quit")
}
