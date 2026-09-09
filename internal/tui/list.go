package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// listPageSize is how many entities one page holds. It is deliberately larger
// than a terminal shows so scrolling stays inside the page.
const listPageSize = 200

// listScreen is the home screen: a paged, kind-filtered table of fleet
// entities. Whatever row is highlighted is the argument every verb receives.
type listScreen struct {
	tbl       table.Model
	kindIx    int
	entities  []fleet.EntityRef
	total     int
	shown     int
	truncated bool
	loadErr   error

	filterText string
	input      textinput.Model
	typing     bool
}

// kinds is the tab order. Index 0 is the everything tab, represented by the
// empty kind so the filter can pass it through untouched.
func (l *listScreen) kinds() []fleet.EntityKind {
	return []fleet.EntityKind{
		"", fleet.KindService, fleet.KindRevision,
		fleet.KindTarget, fleet.KindOwner, fleet.KindSource,
	}
}

func newListScreen(c *Context) screen {
	ti := textinput.New()
	ti.Placeholder = "filter"
	// The width is set again on every WindowSizeMsg; this is only the value
	// before the first resize arrives.
	ti.SetWidth(40)
	l := &listScreen{
		tbl: table.New(
			table.WithColumns(listColumns()),
			table.WithFocused(true),
		),
		input: ti,
	}
	l.refresh(c)
	return l
}

func listColumns() []table.Column {
	return []table.Column{
		{Title: "KIND", Width: 9},
		{Title: "NAME", Width: 34},
		{Title: "STATUS", Width: 14},
		{Title: "DETAIL", Width: 40},
	}
}

// filter is the query this screen's current tab and filter text describe.
func (l *listScreen) filter() fleet.EntityFilter {
	f := fleet.EntityFilter{Text: l.filterText, Limit: listPageSize}
	if k := l.kinds()[l.kindIx]; k != "" {
		f.Kinds = []fleet.EntityKind{k}
	}
	return f
}

// load runs f and reloads the table from the result. A query error empties the
// table and is surfaced by View rather than dropped.
func (l *listScreen) load(c *Context, f fleet.EntityFilter) {
	list, err := c.Query.Entities(f)
	l.loadErr = err
	if err != nil {
		l.entities, l.total, l.shown, l.truncated = nil, 0, 0, false
		l.tbl.SetRows(nil)
		return
	}
	l.entities = list.Entities
	l.total, l.shown, l.truncated = list.Total, list.Count, list.Truncated
	rows := make([]table.Row, 0, len(list.Entities))
	for _, e := range list.Entities {
		rows = append(rows, table.Row{
			string(e.Kind),
			e.Label,
			statusStyle(e.Status).Render(e.Status),
			e.Secondary,
		})
	}
	l.tbl.SetRows(rows)
	l.tbl.SetCursor(0)
}

func (l *listScreen) refresh(c *Context) { l.load(c, l.filter()) }

// selected returns the highlighted entity. ok is false when the list is empty.
func (l *listScreen) selected() (fleet.EntityRef, bool) {
	i := l.tbl.Cursor()
	if i < 0 || i >= len(l.entities) {
		return fleet.EntityRef{}, false
	}
	return l.entities[i], true
}

// capturesText reports that the filter input owns every printable key. globalKey
// checks for this so that typing "q" into the filter does not pop the screen.
func (l *listScreen) capturesText() bool { return l.typing }

func (l *listScreen) Title() string { return "Fleet" }

func (l *listScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		l.resize(c)
		return l, nil
	case tea.KeyPressMsg:
		if l.typing {
			switch msg.String() {
			case "enter":
				l.typing = false
				l.input.Blur()
				l.filterText = l.input.Value()
				l.refresh(c)
				return l, nil
			case "esc":
				// Cancel without applying: the half-typed value is discarded and
				// whatever filter was already applied stays applied.
				l.typing = false
				l.input.Blur()
				l.input.SetValue(l.filterText)
				return l, nil
			}
			in, cmd := l.input.Update(msg)
			l.input = in
			return l, cmd
		}
		switch msg.String() {
		case "enter":
			ref, ok := l.selected()
			if !ok {
				return l, nil
			}
			return l, push(newDetailScreen(c, ref))
		case "/":
			l.typing = true
			return l, l.input.Focus()
		case "esc":
			if l.filterText == "" {
				return l, nil
			}
			l.filterText = ""
			l.input.SetValue("")
			l.refresh(c)
			return l, nil
		case "a":
			return l, push(newAttentionScreen(c))
		case "g":
			ref, ok := l.selected()
			if !ok {
				return l, nil
			}
			return l, push(newGraphScreen(c, ref))
		case "tab":
			l.kindIx = (l.kindIx + 1) % len(l.kinds())
			l.refresh(c)
			return l, nil
		case "shift+tab":
			l.kindIx = (l.kindIx - 1 + len(l.kinds())) % len(l.kinds())
			l.refresh(c)
			return l, nil
		}
	}
	l.resize(c)
	tbl, cmd := l.tbl.Update(msg)
	l.tbl = tbl
	return l, cmd
}

// resize keeps the table inside the frame. The table renders nothing until it
// has a width, so this must run before the first View.
func (l *listScreen) resize(c *Context) {
	l.tbl.SetWidth(c.Width)
	// header + tab strip + filter line + summary line + footer.
	h := c.Height - 5
	if h < 3 {
		h = 3
	}
	l.tbl.SetHeight(h)
}

func (l *listScreen) View(c *Context) string {
	if l.loadErr != nil {
		return errorStyle.Render("query failed: " + l.loadErr.Error())
	}
	l.resize(c)
	return l.tabs() + "\n" + l.filterLine() + "\n" + l.tbl.View() + "\n" + l.summary()
}

func (l *listScreen) tabs() string {
	names := []string{"All", "Services", "Revisions", "Targets", "Owners", "Sources"}
	parts := make([]string, 0, len(names))
	for i, n := range names {
		if i == l.kindIx {
			parts = append(parts, focusStyle.Render("["+n+"]"))
			continue
		}
		parts = append(parts, dimStyle.Render(" "+n+" "))
	}
	return strings.Join(parts, " ")
}

func (l *listScreen) filterLine() string {
	if l.typing {
		return l.input.View()
	}
	if l.filterText != "" {
		return dimStyle.Render("filter: " + l.filterText + "  (esc clears)")
	}
	return dimStyle.Render("/ filter   a attention   tab kind")
}

// summary states the page bounds. A truncated page never presents itself as the
// whole answer.
func (l *listScreen) summary() string {
	if l.truncated {
		return warnStyle.Render(fmt.Sprintf("showing %d of %d — narrow with / to see the rest", l.shown, l.total))
	}
	return dimStyle.Render(fmt.Sprintf("%d entities", l.total))
}
