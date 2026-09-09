package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
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

	// filter and attention are wired in Task 9.
	filterText string
	attention  bool
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
	l := &listScreen{
		tbl: table.New(
			table.WithColumns(listColumns()),
			table.WithFocused(true),
		),
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

// refresh re-runs the query and reloads the table.
func (l *listScreen) refresh(c *Context) {
	f := fleet.EntityFilter{Text: l.filterText, Limit: listPageSize}
	if k := l.kinds()[l.kindIx]; k != "" {
		f.Kinds = []fleet.EntityKind{k}
	}
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

// selected returns the highlighted entity. ok is false when the list is empty.
func (l *listScreen) selected() (fleet.EntityRef, bool) {
	i := l.tbl.Cursor()
	if i < 0 || i >= len(l.entities) {
		return fleet.EntityRef{}, false
	}
	return l.entities[i], true
}

func (l *listScreen) Title() string { return "Fleet" }

func (l *listScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		l.resize(c)
		return l, nil
	case tea.KeyPressMsg:
		switch msg.String() {
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
	// header + tab strip + summary line + footer.
	h := c.Height - 4
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
	return l.tabs() + "\n" + l.tbl.View() + "\n" + l.summary()
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

// summary states the page bounds. A truncated page never presents itself as the
// whole answer.
func (l *listScreen) summary() string {
	if l.truncated {
		return warnStyle.Render(fmt.Sprintf("showing %d of %d — narrow with / to see the rest", l.shown, l.total))
	}
	return dimStyle.Render(fmt.Sprintf("%d entities", l.total))
}
