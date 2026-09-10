package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// confirmScreen gates every write verb. It shows the literal argv that will
// run, because the whole point of "the selection becomes the argument" is that
// the argument was chosen by pointing, and pointing is easy to get wrong by one
// row.
//
// Only lowercase y confirms. Enter, space and capital Y are ignored on purpose:
// a gate that accepts whatever key you were already pressing is not a gate.
type confirmScreen struct {
	prompt string
	argv   []string
	onYes  func() tea.Cmd
}

func newConfirmScreen(prompt string, argv []string, onYes func() tea.Cmd) screen {
	return &confirmScreen{prompt: prompt, argv: argv, onYes: onYes}
}

func (s *confirmScreen) Title() string { return "Confirm" }

func (s *confirmScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return s, nil
	}
	switch k.String() {
	case "y":
		if s.onYes == nil {
			return s, pop()
		}
		return s, tea.Batch(pop(), s.onYes())
	case "n":
		return s, pop()
	}
	return s, nil
}

// View sanitises here rather than at the runWrite call sites, so a write verb
// added later cannot forget: both the prompt and the argv are built from fleet
// content, and safeText explains what those bytes can do to this screen.
func (s *confirmScreen) View(c *Context) string {
	argv := make([]string, len(s.argv))
	for i, tok := range s.argv {
		argv[i] = safeText(tok)
	}
	var b strings.Builder
	b.WriteString(warnStyle.Render(safeText(s.prompt)) + "\n\n")
	b.WriteString("  " + focusStyle.Render(strings.Join(argv, " ")) + "\n\n")
	b.WriteString(dimStyle.Render("  y: run it    n: cancel    (only lowercase y confirms)"))
	return b.String()
}
