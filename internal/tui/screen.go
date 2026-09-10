package tui

import tea "charm.land/bubbletea/v2"

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

// loadingScreen is the initial screen: it shows nothing but a message while the
// fleet snapshot is assembled.
type loadingScreen struct{ note string }

func (l loadingScreen) Title() string { return "Loading" }

func (l loadingScreen) View(c *Context) string {
	if l.note != "" {
		return "Building the fleet snapshot...\n\n  " + safeText(l.note)
	}
	return "Building the fleet snapshot..."
}

func (l loadingScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	if _, ok := msg.(depResolvedMsg); ok {
		l.note = "resolved a dependency"
		return l, nil
	}
	return l, nil
}
