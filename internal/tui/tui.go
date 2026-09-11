// Package tui is the full-screen terminal front-end over the pacto CLI. It
// loads one fleet snapshot, lets you navigate it and runs the CLI's verbs
// against whatever is selected — reads in-process against the snapshot, writes
// by shelling out to the real pacto binary.
package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	// Anim enables motion. The CLI sets it from the same animationsEnabled check
	// the spinner uses, so --no-anim, PACTO_NO_ANIM and a non-tty stdout all turn
	// it off here too. Off is also the default for a zero Options, which is what
	// keeps every test's frame deterministic without opting out.
	Anim bool
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
	// Anim is whether motion is enabled at all, and Frame is the animation clock:
	// a count of frames that only ever rises, advanced by the root Model when a
	// frameMsg lands. Every animation in this package is a pure function of Frame
	// and the frame it started on, so nothing holds its own timer, everything
	// stays on one beat, and a test drives the whole thing by assigning an int.
	Anim  bool
	Frame int
	// pendingDiff and pendingImpact hold the left-hand side of a two-selection
	// verb between the two keypresses that make it up. They live no longer than
	// the snapshot they were armed against: a reload clears them, because the
	// left-hand side may not exist in the new world and the status line that
	// announced the arming is cleared with it.
	pendingDiff   Selection
	pendingImpact Selection
}

// transitionFrames is how long a screen change takes to wipe into place: four
// frames, about a quarter second. Long enough to see where the new screen came
// from, short enough that a reader holding enter never waits on it.
const transitionFrames = 4

// Model is the root tea.Model: a screen stack plus the shared context.
type Model struct {
	ctx   *Context
	stack []screen
	err   error
	// ticking is whether the animation clock is currently armed. Every arming
	// site goes through armTick, which checks this: two live tick chains would
	// advance Frame at double rate and every animation with it.
	ticking bool
	// transitionStart is the frame the current screen change began on.
	transitionStart int
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
			Anim:       o.Anim,
		},
		stack: []screen{loadingScreen{}},
	}
}

// top returns the screen that currently owns the keyboard.
func (m *Model) top() screen { return m.stack[len(m.stack)-1] }

// Init kicks off the snapshot load and starts the clock.
func (m *Model) Init() tea.Cmd { return tea.Batch(loadSnapshot(m.ctx), m.armTick()) }

// transitionProgress is how far the current screen change has run, 0 to 1. With
// animation off it is always 1: the transition is over before it starts, which
// is what makes every render path below collapse to the un-animated frame.
func (m *Model) transitionProgress() float64 {
	if !m.ctx.Anim {
		return 1
	}
	return progressAt(m.ctx.Frame, m.transitionStart, transitionFrames)
}

// animating reports whether anything on screen is still moving. Only the top
// screen is polled because only the top screen is drawn.
func (m *Model) animating() bool {
	if !m.ctx.Anim {
		return false
	}
	return m.transitionProgress() < 1 || screenAnimating(m.top(), m.ctx)
}

// armTick starts the clock if something needs it and it is not already running.
// Returning nil when there is nothing to animate is the whole point: an idle
// TUI schedules no work at all, so a fleet with nothing wrong sits at zero CPU
// rather than repainting sixteen times a second to show the same frame.
func (m *Model) armTick() tea.Cmd {
	if m.ticking || !m.animating() {
		return nil
	}
	m.ticking = true
	return tick()
}

// beginTransition restarts the wipe and makes sure the clock is running.
func (m *Model) beginTransition() tea.Cmd {
	m.transitionStart = m.ctx.Frame
	return m.armTick()
}

// applySnapshot swaps in a snapshot that has finished loading and re-renders
// whatever is on screen against it.
func (m *Model) applySnapshot(msg snapshotMsg) tea.Cmd {
	if msg.err != nil {
		m.err = msg.err
		return nil
	}
	m.ctx.Query = fleet.NewQuery(msg.snap)
	m.ctx.Snapshot = msg.snap
	// The snapshot is the world every screen was rendered from, so a fresh one
	// clears both the progress note and any error left over from the write that
	// asked for it. An armed d or i goes with them: the status line was its only
	// indicator, and its left-hand side may not exist in the world that just
	// arrived. Left set, it ambushes the next d or i with a comparison against a
	// revision the reader stopped looking at several minutes ago.
	m.err = nil
	m.ctx.Status = ""
	m.ctx.pendingDiff, m.ctx.pendingImpact = Selection{}, Selection{}
	if _, starting := m.top().(loadingScreen); starting {
		// Replace rather than push: the loading screen is not somewhere the user
		// can go back to.
		m.stack[len(m.stack)-1] = newListScreen(m.ctx)
		return m.beginTransition()
	}
	m.reloadScreens()
	return m.beginTransition()
}

// Update routes global keys and stack navigation, then delegates to the top
// screen. Global keys are handled first and are not forwarded.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case frameMsg:
		// The clock, and the only place Frame advances. Re-arming here rather
		// than unconditionally is what stops it: the moment nothing is moving the
		// chain ends and no further frames are scheduled.
		m.ctx.Frame++
		if m.animating() {
			return m, tick()
		}
		m.ticking = false
		return m, nil
	case tea.WindowSizeMsg:
		m.ctx.Width, m.ctx.Height = msg.Width, msg.Height
	case snapshotMsg:
		return m, m.applySnapshot(msg)
	case pushMsg:
		m.stack = append(m.stack, msg.s)
		return m, m.beginTransition()
	case popMsg:
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
		}
		return m, m.beginTransition()
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
	// A screen can start its own animation from inside Update -- the graph
	// re-walks itself on a depth change -- and has no way to schedule a frame,
	// because the clock belongs to the root. Arming here covers all of them, and
	// costs nothing when the screen did not start anything: armTick asks
	// animating() first.
	return m, tea.Batch(cmd, m.armTick())
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
	body := revealLines(m.top().View(m.ctx), m.transitionProgress())
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
	const prefix = "pacto  "
	const tag = "  [read-only]"
	room := m.ctx.Width - lipgloss.Width(prefix)
	if m.ctx.ReadOnly {
		room -= lipgloss.Width(tag)
	}
	title := headerStyle.Render(prefix + breadcrumb(crumbs, room))
	if m.ctx.ReadOnly {
		title += "  " + warnStyle.Render("[read-only]")
	}
	return title
}

// breadcrumb renders the trail in at most width cells. Every push adds a crumb
// and nothing ever dropped one, so twelve g presses gave a 285-column trail on
// an 80-column terminal and the renderer wrapped it over the body.
//
// What goes is the middle: the first crumb says where the reader started and the
// last says where they are now, and the trail between the two is what a narrow
// terminal can afford to lose. A first-and-last that still does not fit is cut
// rather than wrapped -- a header that eats the screen is worse than a clipped
// one.
func breadcrumb(crumbs []string, width int) string {
	trail := strings.Join(crumbs, " > ")
	if lipgloss.Width(trail) <= width {
		return trail
	}
	if len(crumbs) > 2 {
		trail = crumbs[0] + " > ... > " + crumbs[len(crumbs)-1]
		if lipgloss.Width(trail) <= width {
			return trail
		}
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(trail)
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
