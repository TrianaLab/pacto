package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// attentionScreen lists what the fleet says needs looking at, filtered by
// category. It is a peer of the list screen, not a mode of it: the rows are
// findings rather than entities, and each one carries its own next step.
type attentionScreen struct {
	tbl     table.Model
	catIx   int
	items   []fleet.AttentionItem
	loadErr error
}

// attentionCategories returns the category tabs: an "All" sentinel followed by
// a COPY of the package list. fleet.AttentionCategories is a mutable package
// var, so it is never indexed, sorted or appended to in place.
func attentionCategories() []string {
	return append([]string{""}, fleet.AttentionCategories...)
}

func newAttentionScreen(c *Context) screen {
	a := &attentionScreen{
		tbl: table.New(
			table.WithColumns([]table.Column{
				{Title: "SEVERITY", Width: 10},
				{Title: "SERVICE", Width: 28},
				{Title: "CATEGORY", Width: 14},
				{Title: "SUMMARY", Width: 44},
			}),
			table.WithFocused(true),
		),
	}
	a.refresh(c)
	return a
}

// filter is the query this screen's current category describes.
func (a *attentionScreen) filter() fleet.AttentionFilter {
	f := fleet.AttentionFilter{}
	if cat := attentionCategories()[a.catIx]; cat != "" {
		f.Category = cat
	}
	return f
}

// load runs f and reloads the table from the result. A query error empties the
// table and is surfaced by View rather than dropped.
func (a *attentionScreen) load(c *Context, f fleet.AttentionFilter) {
	list, err := c.Query.Attention(f)
	a.loadErr = err
	if err != nil {
		a.items = nil
		a.tbl.SetRows(nil)
		return
	}
	a.items = list.Items
	rows := make([]table.Row, 0, len(list.Items))
	for _, it := range list.Items {
		rows = append(rows, table.Row{
			statusStyle(it.Severity).Render(safeText(it.Severity)),
			safeText(it.Service),
			safeText(it.Category),
			safeText(it.Summary),
		})
	}
	a.tbl.SetRows(rows)
	a.tbl.SetCursor(0)
}

func (a *attentionScreen) refresh(c *Context) { a.load(c, a.filter()) }

// selected returns the entity the highlighted finding is about, so the verbs
// work here exactly as they do on the list.
func (a *attentionScreen) selected() (fleet.EntityRef, bool) {
	i := a.tbl.Cursor()
	if i < 0 || i >= len(a.items) {
		return fleet.EntityRef{}, false
	}
	return a.items[i].Entity, true
}

func (a *attentionScreen) Title() string { return "Attention" }

// bindings are the keys Update handles below, in the order the help lists them.
func (a *attentionScreen) bindings() []binding {
	return []binding{
		{Key: "enter", Help: "open the entity the finding is about"},
		{Key: "tab", Help: "next category tab"},
		{Key: "shift+tab", Help: "previous category tab"},
	}
}

func (a *attentionScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		if cmd, handled := dispatchVerb(c, a, k); handled {
			return a, cmd
		}
		switch k.String() {
		case "enter":
			ref, ok := a.selected()
			if !ok {
				return a, nil
			}
			return a, push(newDetailScreen(c, ref))
		case "tab":
			a.catIx = (a.catIx + 1) % len(attentionCategories())
			a.refresh(c)
			return a, nil
		case "shift+tab":
			n := len(attentionCategories())
			a.catIx = (a.catIx - 1 + n) % n
			a.refresh(c)
			return a, nil
		}
	}
	a.resize(c)
	tbl, cmd := a.tbl.Update(msg)
	a.tbl = tbl
	return a, cmd
}

func (a *attentionScreen) resize(c *Context) {
	a.tbl.SetWidth(c.Width)
	h := c.Height - 5
	if h < 3 {
		h = 3
	}
	a.tbl.SetHeight(h)
}

func (a *attentionScreen) View(c *Context) string {
	if a.loadErr != nil {
		return errorStyle.Render("attention query failed: " + a.loadErr.Error())
	}
	a.resize(c)
	head := a.tabs()
	if len(a.items) == 0 {
		return head + "\n\n" + dimStyle.Render("Nothing in this category needs attention.")
	}
	return head + "\n" + a.tbl.View() + "\n" + a.nextStep()
}

func (a *attentionScreen) tabs() string {
	cats := attentionCategories()
	parts := make([]string, 0, len(cats))
	for i, cat := range cats {
		name := cat
		if name == "" {
			name = "all"
		}
		if i == a.catIx {
			parts = append(parts, focusStyle.Render("["+name+"]"))
			continue
		}
		parts = append(parts, dimStyle.Render(" "+name+" "))
	}
	return strings.Join(parts, " ")
}

// nextStep shows the highlighted finding's remedy, which is the only part of an
// attention item a table row cannot carry.
func (a *attentionScreen) nextStep() string {
	i := a.tbl.Cursor()
	if i < 0 || i >= len(a.items) {
		return ""
	}
	it := a.items[i]
	if it.NextStep == "" {
		return dimStyle.Render(safeText(it.Reason))
	}
	return dimStyle.Render(fmt.Sprintf("%s  ->  %s", safeText(it.Reason), safeText(it.NextStep)))
}
