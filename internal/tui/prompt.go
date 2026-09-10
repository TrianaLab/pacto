package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// promptScreen asks for the one argument a selection cannot supply: a registry
// reference to push to, a plugin name to run. A contract declares neither, so
// there is nothing in the fleet to read them out of.
//
// It is deliberately not a form. One value, enter to continue, esc to cancel,
// and the answer goes straight to the confirmation screen, which is still the
// only thing that can start a write.
type promptScreen struct {
	question string
	input    textinput.Model
	onSubmit func(answer string) tea.Cmd
}

// promptFor opens a prompt and focuses its input in the same command. Focus
// returns the cursor-blink command, so a constructor that could only hand back a
// screen would have to drop it and the prompt would look dead.
func promptFor(question string, onSubmit func(answer string) tea.Cmd) tea.Cmd {
	p := &promptScreen{question: question, input: textinput.New(), onSubmit: onSubmit}
	p.input.SetWidth(60)
	return tea.Batch(push(p), p.input.Focus())
}

func (p *promptScreen) Title() string { return "Prompt" }

// capturesText keeps globalKey off every printable key while the reader types,
// so a reference containing a q or a ? does not pop the screen out from under
// them mid-word.
func (p *promptScreen) capturesText() bool { return true }

func (p *promptScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "enter":
			answer := strings.TrimSpace(p.input.Value())
			if answer == "" {
				return p, status("type a value, or press esc to cancel")
			}
			// Sequence rather than Batch: the pop has to land before onSubmit
			// pushes the confirmation, and Batch's commands race.
			return p, tea.Sequence(pop(), p.onSubmit(answer))
		case "esc":
			return p, pop()
		}
	}
	in, cmd := p.input.Update(msg)
	p.input = in
	return p, cmd
}

func (p *promptScreen) View(c *Context) string {
	var b strings.Builder
	b.WriteString(warnStyle.Render(p.question) + "\n\n")
	// safeFragment, not safeText: the input renders its own cursor styling, and
	// its value is whatever was pasted into it. A reader cannot type a control
	// character but a paste carries one, and this prompt's answer becomes an
	// argv token on the confirmation screen.
	b.WriteString("  " + safeFragment(p.input.View()) + "\n\n")
	b.WriteString(dimStyle.Render("  enter: continue    esc: cancel"))
	return b.String()
}
