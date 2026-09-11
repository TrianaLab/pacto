package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// helpScreen lists the global keys, the keys of the screen below it and the
// verbs available there. It is generated from the binding tables, never
// hand-maintained.
//
// under is the screen the reader pressed ? on. Without it the help could only
// ever show the global keys and the verbs, which left out tab, shift+tab, /,
// enter, a and the depth keys -- the whole navigation keymap, and precisely the
// part a first-time reader has no other way to discover.
type helpScreen struct{ under screen }

func (h helpScreen) Title() string { return "Help" }

func (h helpScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) { return h, nil }

func (h helpScreen) View(c *Context) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Keys") + "\n")
	for _, k := range globalBindings() {
		b.WriteString("  " + focusStyle.Render(pad(k.Key, 8)) + k.Help + "\n")
	}
	if s, ok := h.under.(screenBindings); ok {
		// safeText because a detail screen's title is the entity's own label.
		b.WriteString("\n" + headerStyle.Render("On "+safeText(h.under.Title())) + "\n")
		for _, k := range s.bindings() {
			b.WriteString("  " + focusStyle.Render(pad(k.Key, 8)) + k.Help + "\n")
		}
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
