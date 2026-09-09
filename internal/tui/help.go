package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// helpScreen lists the global keys and the verbs available on the screen below
// it. It is generated from the binding tables, never hand-maintained.
type helpScreen struct{}

func (h helpScreen) Title() string { return "Help" }

func (h helpScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) { return h, nil }

func (h helpScreen) View(c *Context) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Keys") + "\n")
	for _, k := range globalBindings() {
		b.WriteString("  " + focusStyle.Render(pad(k.Key, 8)) + k.Help + "\n")
	}
	b.WriteString("\n" + headerStyle.Render("Verbs") + "\n")
	for _, v := range verbList(c) {
		line := "  " + focusStyle.Render(pad(v.Key, 8)) + v.Help
		if v.Write {
			line += dimStyle.Render("  (writes; asks first)")
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// pad right-pads s to n columns.
func pad(s string, n int) string {
	if len(s) >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-len(s))
}
