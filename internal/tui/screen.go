package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// screen is one full-terminal view. Screens are pushed and popped on a stack:
// the top screen owns the keyboard, everything below it is suspended state a
// pop returns to. Screens are values, not tea.Models, because they all share
// one *Context and returning the next screen makes an in-place mutation
// impossible to write by accident.
type screen interface {
	// Update handles one message and returns the screen that should be on top
	// afterwards. Returning the receiver means "unchanged".
	Update(c *Context, msg tea.Msg) (screen, tea.Cmd)
	// View renders the screen body. The root Model draws the header and footer.
	View(c *Context) string
	// Title names the screen in the header and in the breadcrumb.
	Title() string
}

// push returns a command that pushes s onto the stack.
func push(s screen) tea.Cmd { return func() tea.Msg { return pushMsg{s: s} } }

// pop returns a command that pops the top screen.
func pop() tea.Cmd { return func() tea.Msg { return popMsg{} } }

// loadingScreen is the initial screen: a spinner and a sweeping bar while the
// fleet snapshot is assembled.
//
// It carries no state. It used to hold a progress note set from depResolvedMsg,
// but nothing can deliver one here: the only producer is the lock verb's
// OnDepResolved hook, which runs under an output screen pushed on top, and
// Model.Update routes to the top screen only. This screen exists solely before
// the first snapshot lands, which is before any verb can be dispatched.
type loadingScreen struct{}

func (l loadingScreen) Title() string { return "Loading" }

// animating is true for as long as this screen is up. A load is by definition
// unfinished work, so there is always something to say about it.
func (l loadingScreen) animating(c *Context) bool { return true }

func (l loadingScreen) View(c *Context) string {
	const indent = "  "
	head := indent + headerStyle.Render("Building the fleet snapshot")
	if c.Anim {
		head = indent + focusStyle.Render(spinnerAt(c.Frame)) + "  " +
			headerStyle.Render("Building the fleet snapshot")
	}
	lines := []string{"", head, ""}
	if c.Anim {
		w := c.Width - len(indent)*2
		if w > 40 {
			w = 40
		}
		lines = append(lines, indent+faintStyle.Render(sweep(c.Frame, w)), "")
	}
	return strings.Join(lines, "\n")
}

func (l loadingScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) { return l, nil }
