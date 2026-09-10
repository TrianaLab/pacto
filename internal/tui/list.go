package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// listPageSize is how many entities one page holds. It is deliberately larger
// than a terminal shows so scrolling stays inside the page.
const listPageSize = 200

// Column widths. STATUS fits the longest badge ("● Non-compliant"); NAME and
// DETAIL take whatever is left.
const (
	statusColWidth = 15
	kindColWidth   = 8
	minNameWidth   = 24
)

// listScreen is the home screen: a health banner over a kind-filtered table,
// with a live detail pane beside it. Whatever row is highlighted is both what
// the pane describes and the argument every verb receives.
type listScreen struct {
	tbl       table.Model
	kindIx    int
	entities  []fleet.EntityRef
	total     int
	shown     int
	truncated bool
	loadErr   error

	banner healthBanner
	// attention is whether any loaded row is a confirmed problem. It is what
	// decides whether this screen animates at all: with nothing wrong there is
	// nothing to throb, and the clock stops.
	attention bool

	filterText string
	input      textinput.Model
	typing     bool
}

// servicesKindIx is the tab the session opens on: index 1 of kinds(), Services.
//
// Not the All tab, even though it is index 0. sortEntityRefs orders kind-first
// and alphabetically -- owner < revision < service < source < target -- so on a
// fleet with 200-plus owners and revisions between them, All's first page ends
// before the first service row and the landing screen shows no services at all,
// at exactly the fleet size this feature exists for. Services is also the tab
// the reader wanted; All is one shift+tab away.
const servicesKindIx = 1

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
		tbl:    pactoTable(listColumns(80, true, true)),
		kindIx: servicesKindIx,
		input:  ti,
	}
	l.refresh(c)
	return l
}

// listColumns sizes the four columns to a table tw cells wide. A column with
// width 0 is skipped by the table entirely, which is how KIND and DETAIL come
// and go without the row builder ever changing shape: rows are always four
// cells in this order, and the layout decides which of them are drawn.
func listColumns(tw int, showKind, showDetail bool) []table.Column {
	// Every column costs its width plus one cell of padding on each side.
	cols, fixed := 2, statusColWidth
	kind := 0
	if showKind {
		kind = kindColWidth
		cols++
		fixed += kind
	}
	if showDetail {
		cols++
	}
	flex := tw - 2*cols - fixed
	if flex < minNameWidth {
		flex = minNameWidth
	}
	name, detail := flex, 0
	if showDetail {
		detail = flex * 2 / 5
		name = flex - detail
	}
	return []table.Column{
		{Title: "KIND", Width: kind},
		{Title: "NAME", Width: name},
		{Title: "STATUS", Width: statusColWidth},
		{Title: "DETAIL", Width: detail},
	}
}

// paneWidth is how wide the detail pane gets, or 0 when the terminal is too
// narrow to split. Below the threshold the table takes the whole width and
// grows a DETAIL column back, so a narrow terminal loses the layout but not the
// information.
func paneWidth(total int) int {
	if total < 76 {
		return 0
	}
	if w := total / 3; w < 40 {
		return w
	}
	return 40
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
	if l.banner.stale(c) {
		l.banner = newHealthBanner(c)
	}
	list, err := c.Query.Entities(f)
	l.loadErr = err
	if err != nil {
		l.entities, l.total, l.shown, l.truncated = nil, 0, 0, false
		l.attention = false
		l.tbl.SetRows(nil)
		return
	}
	l.entities = list.Entities
	l.total, l.shown, l.truncated = list.Total, list.Count, list.Truncated
	l.attention = false
	for _, e := range list.Entities {
		if needsAttention(e.Status) {
			l.attention = true
			break
		}
	}
	l.tbl.SetRows(l.rows(c))
	l.tbl.SetCursor(0)
}

// rows renders the loaded entities. It is called again on every animated frame,
// because the status badge of a confirmed problem is drawn at the pulse's
// current brightness and a table row is a plain string once built.
func (l *listScreen) rows(c *Context) []table.Row {
	rows := make([]table.Row, 0, len(l.entities))
	for _, e := range l.entities {
		rows = append(rows, table.Row{
			safeText(string(e.Kind)),
			safeText(e.Label),
			l.badge(c, e.Status),
			safeText(e.Secondary),
		})
	}
	return rows
}

// badge is one row's status cell: the coloured glyph and the words, pulsing if
// the status is a confirmed problem and animation is on.
func (l *listScreen) badge(c *Context, s string) string {
	if s == "" {
		return ""
	}
	p := statusPresentationFor(s)
	return pulseStyle(c, s).Render(p.Glyph + " " + p.Label)
}

func (l *listScreen) refresh(c *Context) { l.load(c, l.filter()) }

// animating reports whether this screen has motion in flight: a banner figure
// still counting up, or a row that needs attention and is throbbing to say so.
func (l *listScreen) animating(c *Context) bool {
	if !c.Anim {
		return false
	}
	return l.attention || l.banner.animating(c) ||
		progressAt(c.Frame, l.banner.startFrame, framesFor(countUpDuration)) < 1
}

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

// ownsEscape reports that esc has a job here: clearing the applied filter,
// which is what the hint bar has always told the reader it does. It is narrower
// than capturesText on purpose — that one stands globalKey down for every
// binding but ctrl+c, so reusing it here would take q, r and ? away too, for as
// long as a filter was applied.
func (l *listScreen) ownsEscape() bool { return l.filterText != "" }

func (l *listScreen) Title() string { return "Fleet" }

// bindings are the keys Update handles below, in the order the help lists them.
func (l *listScreen) bindings() []binding {
	return []binding{
		{Key: "up/down, j/k", Help: "move the highlight"},
		{Key: "pgup/pgdn, home/end", Help: "move by a page, or to either end"},
		{Key: "enter", Help: "open the highlighted entity"},
		{Key: "/", Help: "filter; enter applies, esc discards"},
		{Key: "esc", Help: "clear an applied filter"},
		{Key: "a", Help: "what needs attention"},
		{Key: "tab", Help: "next kind tab"},
		{Key: "shift+tab", Help: "previous kind tab"},
	}
}

// typingKey handles a key press while the filter input has focus. Enter applies
// what was typed, esc discards it and leaves whatever filter was already
// applied in place, and every other key belongs to the input.
func (l *listScreen) typingKey(c *Context, msg tea.KeyPressMsg) (screen, tea.Cmd) {
	switch msg.String() {
	case "enter":
		l.typing = false
		l.input.Blur()
		l.filterText = l.input.Value()
		l.refresh(c)
		return l, nil
	case "esc":
		l.typing = false
		l.input.Blur()
		l.input.SetValue(l.filterText)
		return l, nil
	}
	in, cmd := l.input.Update(msg)
	l.input = in
	return l, cmd
}

func (l *listScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		l.resize(c)
		return l, nil
	case tea.KeyPressMsg:
		if l.typing {
			return l.typingKey(c, msg)
		}
		if cmd, handled := dispatchVerb(c, l, msg); handled {
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
			// Reached only when ownsEscape() claimed the key, which is exactly
			// when a filter is applied: with none, globalKey binds esc to
			// quitOrPop and never delegates it here. So there is no empty-filter
			// case to guard — the guard that used to sit here was reachable only
			// from a test calling this method directly.
			l.filterText = ""
			l.input.SetValue("")
			l.refresh(c)
			return l, nil
		case "a":
			return l, push(newAttentionScreen(c))
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

// bodyHeight is how many rows the split gets: everything the frame is not
// already spending on the banner, the tabs, the hint bar and the root model's
// own header and footer.
func (l *listScreen) bodyHeight(c *Context) int {
	if h := c.Height - 6; h > 3 {
		return h
	}
	return 3
}

// resize keeps the table inside the frame. The table renders nothing until it
// has a width, so this must run before the first View.
func (l *listScreen) resize(c *Context) {
	tw := c.Width
	pane := paneWidth(c.Width)
	if pane > 0 {
		tw = c.Width - pane - 1
	}
	l.tbl.SetColumns(listColumns(tw, l.kindIx == 0, pane == 0))
	l.tbl.SetWidth(tw)
	l.tbl.SetHeight(l.bodyHeight(c))
	l.input.SetWidth(c.Width - 4)
}

func (l *listScreen) View(c *Context) string {
	if l.loadErr != nil {
		return errorStyle.Render("query failed: " + l.loadErr.Error())
	}
	l.resize(c)
	h := l.bodyHeight(c)
	body := l.split(c, h)
	if l.total == 0 {
		body = padLines(l.empty(c), h)
	}
	return l.banner.view(c) + "\n" + l.tabs() + "\n" + body + "\n" + l.hintBar(c)
}

// split renders the table with the detail pane beside it.
func (l *listScreen) split(c *Context, h int) string {
	if c.Anim && l.attention {
		// Re-render the rows so the confirmed-problem badges are drawn at this
		// frame's brightness. Only when something is actually pulsing: a healthy
		// fleet keeps the rows it was built with.
		l.tbl.SetRows(l.rows(c))
	}
	left := padLines(l.tbl.View(), h)
	pane := paneWidth(c.Width)
	if pane == 0 {
		return left
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", l.pane(c, pane, h))
}

// pane describes the highlighted entity beside the list. Everything in it comes
// from the row's own EntityRef, which already carries the domain, scope, owning
// service, version and explanation that the table has never had room for.
func (l *listScreen) pane(c *Context, w, h int) string {
	inner := w - 4 // two border cells, two padding cells
	body := l.paneBody(c, inner)
	return panelStyle.Width(w-2).Height(h-2).Padding(0, 1).Render(body)
}

func (l *listScreen) paneBody(c *Context, w int) string {
	ref, ok := l.selected()
	if !ok {
		return faintStyle.Render("Nothing highlighted.")
	}
	lines := []string{
		focusStyle.Render(trunc(ref.Label, w)),
		faintStyle.Render(string(ref.Kind)),
	}
	if ref.Status != "" {
		lines = append(lines, "", pulseStyle(c, ref.Status).Render(
			statusPresentationFor(ref.Status).Glyph+" "+statusPresentationFor(ref.Status).Label))
	}
	fields := [][2]string{
		{"domain", ref.Domain},
		{"scope", ref.Scope},
		{"service", ref.ParentService},
		{"version", ref.Version},
		{"detail", ref.Secondary},
	}
	rows := make([]string, 0, len(fields))
	for _, f := range fields {
		if f[1] == "" {
			continue
		}
		rows = append(rows, dimStyle.Render(fmt.Sprintf("%-8s", f[0]))+trunc(safeText(f[1]), w-9))
	}
	if len(rows) > 0 {
		lines = append(append(lines, ""), rows...)
	}
	if ref.Explanation != "" {
		lines = append(lines, "", faintStyle.Render("why"),
			lipgloss.NewStyle().Width(w).Render(safeText(ref.Explanation)))
	}
	lines = append(lines, "", hints("enter", "open", "g", "graph", "e", "explain"))
	return strings.Join(lines, "\n")
}

// empty explains an empty list instead of showing a blank grid.
//
// This is the screen a reader most often meets first, because --local defaults
// to the working directory: run pacto tui anywhere without bundles under it and
// every source comes back with nothing. What used to render was a header, a
// column strip, nineteen blank rows and "0 entities" — no statement of what was
// looked at, no reason and nothing to try next, which is indistinguishable from
// the program being broken. So each of the three ways a list empties gets its
// own answer, and the one that means "no data at all" names every source it
// consulted and what it found there.
func (l *listScreen) empty(c *Context) string {
	if l.filterText != "" {
		return "\n  " + warnStyle.Render("Nothing matches "+safeText(l.filterText)+".") +
			"\n\n  " + hints("esc", "clear the filter", "tab", "try another kind")
	}
	if l.banner.services+l.banner.revisions+l.banner.targets > 0 {
		return "\n  " + dimStyle.Render(fmt.Sprintf("No %s in this snapshot.", l.tabName())) +
			"\n\n  " + hints("tab", "next kind", "shift+tab", "previous kind")
	}
	return l.emptyFleet(c)
}

// emptyFleet is the no-data-at-all case: what was scanned, what it returned and
// what to run instead.
func (l *listScreen) emptyFleet(c *Context) string {
	b := []string{
		"",
		"  " + warnStyle.Render("This snapshot is empty — no source returned anything."),
		"",
		"  " + faintStyle.Render("sources consulted"),
	}
	if len(c.Snapshot.Sources) == 0 {
		b = append(b, "    "+dimStyle.Render("none"))
	}
	for _, s := range c.Snapshot.Sources {
		line := "    " + statusDot(string(s.Status)) + " " +
			dimStyle.Render(fmt.Sprintf("%-10s %-28s %d revisions, %d targets",
				safeText(s.Kind), safeText(s.ID), s.RevisionCount, s.TargetCount))
		if s.Error != nil {
			line += "  " + errorStyle.Render(safeText(s.Error.Message))
		}
		b = append(b, line)
	}
	b = append(b,
		"",
		"  "+dimStyle.Render("It was launched as:  "+safeText(strings.Join(append([]string{"pacto tui"}, c.SourceArgs...), " "))),
		"",
		"  "+faintStyle.Render("point it at something that has contracts"),
		"    "+keyStyle.Render("pacto tui --local ./path/to/bundles"),
		"    "+keyStyle.Render("pacto tui --oci ghcr.io/your-org/pactos/your-service"),
		"    "+keyStyle.Render("pacto tui --namespace default"),
		"",
		"  "+hints("r", "reload", "?", "keys", "q", "quit"),
	)
	return strings.Join(b, "\n")
}

// tabName is the current tab's plural noun, for prose.
func (l *listScreen) tabName() string {
	return strings.ToLower([]string{"entities", "services", "revisions", "targets", "owners", "sources"}[l.kindIx])
}

func (l *listScreen) tabs() string {
	names := []string{"All", "Services", "Revisions", "Targets", "Owners", "Sources"}
	parts := make([]string, 0, len(names))
	for i, n := range names {
		if i == l.kindIx {
			parts = append(parts, focusStyle.Render(glyphArrow+n))
			continue
		}
		parts = append(parts, dimStyle.Render(" "+n))
	}
	return strings.Join(parts, "  ")
}

// hintBar is the bottom line of this screen: the filter input while typing, the
// applied filter when there is one, and otherwise the keys that move and open.
// It also carries the page bounds, so a truncated page never presents itself as
// the whole answer.
func (l *listScreen) hintBar(c *Context) string {
	if l.typing {
		// See prompt.go: the input styles itself, so the frame-safe form of its
		// view is the one that keeps the styling and cleans the rest.
		return safeFragment(l.input.View())
	}
	left := hints("↑↓", "move", "enter", "open", "/", "filter", "tab", "kind", "a", "attention", "?", "keys")
	if l.filterText != "" {
		left = keyStyle.Render("filter") + " " + hintStyle.Render(safeText(l.filterText)) +
			"   " + hints("esc", "clear")
	}
	return spread(left, l.summary(), c.Width)
}

// summary states the page bounds.
func (l *listScreen) summary() string {
	if l.truncated {
		return warnStyle.Render(fmt.Sprintf("%d of %d — narrow with /", l.shown, l.total))
	}
	return faintStyle.Render(fmt.Sprintf("%d %s", l.total, l.tabName()))
}

// padLines grows s to exactly h lines, so the block beside it lines up and the
// footer below it stays where it was. It never shrinks: clipping a pane would
// cut a border and leave the frame open.
func padLines(s string, h int) string {
	if n := strings.Count(s, "\n") + 1; n < h {
		return s + strings.Repeat("\n", h-n)
	}
	return s
}

// trunc cuts s to w cells with an ellipsis, counting display width rather than
// bytes so a multi-byte name is not cut mid-rune.
func trunc(s string, w int) string {
	if w < 1 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(safeText(s))
}
