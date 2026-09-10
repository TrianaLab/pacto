package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// The palette. Indigo is pacto's accent everywhere else it renders -- the CLI
// spinner uses the same #6366F1 -- so the TUI does not invent a second brand.
// The rest are chosen to survive a 256-colour terminal: lipgloss degrades a hex
// colour to the nearest cube entry, and these were picked far enough apart that
// the degraded forms stay distinguishable.
var (
	colIndigo = lipgloss.Color("#6366F1") // accent, focus, selection
	colGreen  = lipgloss.Color("#22C55E") // compliant, available
	colAmber  = lipgloss.Color("#F59E0B") // warning, partial
	colYellow = lipgloss.Color("#EAB308") // unknown, stale
	colOrange = lipgloss.Color("#FB923C") // non-compliant
	colRed    = lipgloss.Color("#EF4444") // invalid, unavailable
	colCyan   = lipgloss.Color("#22D3EE") // reference
	colGrey   = lipgloss.Color("#6B7280") // not evaluated, chrome
	colFaint  = lipgloss.Color("#9CA3AF") // secondary text
)

// Glyphs. A status is a dot rather than a word wherever the word would repeat
// down a column: the colour carries the meaning and the label says it once.
const (
	glyphDot      = "●"
	glyphRing     = "○"
	glyphArrow    = "▸"
	glyphBarFull  = "▰"
	glyphBarEmpty = "▱"
)

// statusPresentation is how one status value renders: a colour, a glyph and the
// words to print when there is room for words.
type statusPresentation struct {
	Color color.Color
	Glyph string
	Label string
	// Rank orders the health bar and the banner's counts, most severe first. It
	// mirrors pkg/fleet's own severity order rather than inventing a second one.
	Rank int
}

// statusTable maps BOTH vocabularies EntityRef.Status can carry. The field is
// polymorphic by kind -- a compliance state for service, revision and target, a
// source-health state for source -- so a table that knows only one of them
// leaves the other unstyled.
//
// This is why the previous mapping rendered nothing: it switched on "compliant",
// "non-compliant" and "invalid" in lower case, and the compliance vocabulary is
// CamelCase (fleet.StatusCompliant is "Compliant"). Of the eleven real values
// exactly one -- "stale" -- ever matched, so every service in the list drew in
// the default style and the column carried no signal at all. Keying this off the
// fleet constants rather than string literals is what stops it drifting again.
var statusTable = map[string]statusPresentation{
	// Compliance, most severe first.
	fleet.StatusInvalid:      {colRed, glyphDot, "Invalid", 0},
	fleet.StatusNonCompliant: {colOrange, glyphDot, "Non-compliant", 1},
	fleet.StatusUnknown:      {colYellow, glyphDot, "Unknown", 2},
	fleet.StatusWarning:      {colAmber, glyphDot, "Warning", 3},
	fleet.StatusCompliant:    {colGreen, glyphDot, "Compliant", 4},
	fleet.StatusReference:    {colCyan, glyphRing, "Reference", 5},
	fleet.StatusNotEvaluated: {colGrey, glyphRing, "Not evaluated", 6},

	// Source health. Ranked alongside the compliance states so a mixed list
	// sorts sensibly; an unavailable source is as bad as an invalid contract.
	string(fleet.SourceUnavailable): {colRed, glyphDot, "Unavailable", 0},
	string(fleet.SourcePartial):     {colAmber, glyphDot, "Partial", 3},
	string(fleet.SourceStale):       {colYellow, glyphDot, "Stale", 2},
	string(fleet.SourceAvailable):   {colGreen, glyphDot, "Available", 4},
}

// pactoTableStyles is the shared table skin. The bubbles default paints the
// selected row hot pink, which belongs to no palette this project has.
func pactoTableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.Bold(true).Foreground(colFaint)
	s.Selected = lipgloss.NewStyle().Bold(true).Background(colIndigo).Foreground(lipgloss.Color("#FFFFFF"))
	return s
}

// pactoTableKeyMap is the shared table keymap, with the letter aliases that
// pacto's verbs already own taken away.
//
// The defaults bind d to half-page-down, g to top and G to bottom. This package
// binds d to diff, g to graph and G to generate, and dispatchVerb runs first --
// so those three paging keys have never worked, and a reader who learned them
// from another bubbles app got a diff prompt instead of a scroll. Leaving them
// bound would be worse than dropping them: the collision is silent, and the
// verb is the more surprising outcome of the two. The control and named forms
// (ctrl+d, ctrl+u, home, end, pgup, pgdn) do everything the letters did.
func pactoTableKeyMap() table.KeyMap {
	k := table.DefaultKeyMap()
	k.HalfPageDown = key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "half page down"))
	k.HalfPageUp = key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "half page up"))
	k.GotoTop = key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "first row"))
	k.GotoBottom = key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "last row"))
	return k
}

// pactoTable builds a table wearing both of the above.
func pactoTable(cols []table.Column) table.Model {
	t := table.New(table.WithColumns(cols), table.WithFocused(true))
	t.SetStyles(pactoTableStyles())
	t.KeyMap = pactoTableKeyMap()
	return t
}

// hints renders a key hint bar from alternating key and description arguments.
// It is the answer to not knowing you can press an arrow: the keys that move
// and open are on screen at all times rather than one ? away.
func hints(kv ...string) string {
	parts := make([]string, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		parts = append(parts, keyStyle.Render(kv[i])+" "+hintStyle.Render(kv[i+1]))
	}
	return strings.Join(parts, "   ")
}

// statusPresentationFor returns how s renders. An unrecognised value renders
// plain and grey, which is the honest default: an unknown status is not a pass.
func statusPresentationFor(s string) statusPresentation {
	if p, ok := statusTable[s]; ok {
		return p
	}
	return statusPresentation{Color: colGrey, Glyph: glyphRing, Label: s, Rank: 99}
}

// statusStyle is the style s renders in.
func statusStyle(s string) style {
	return style{lipgloss.NewStyle().Foreground(statusPresentationFor(s).Color)}
}

// statusDot renders the coloured glyph alone, for a column too narrow for words.
func statusDot(s string) string {
	return statusStyle(s).Render(statusPresentationFor(s).Glyph)
}

// statusBadge renders "<dot> <words>" -- the dot for the eye, the words for a
// reader who has not learned the colours yet and for anyone who cannot see them.
func statusBadge(s string) string {
	if s == "" {
		return ""
	}
	p := statusPresentationFor(s)
	return statusStyle(s).Render(p.Glyph + " " + p.Label)
}

// needsAttention reports whether s is a CONFIRMED bad state -- invalid, non-
// compliant, or a source that is flatly unavailable. It is what the pulsing
// markers key off, and it is deliberately narrower than "not compliant".
//
// Unknown and Stale are excluded even though they rank above Warning. They mean
// the fleet could not observe something, not that it observed a contradiction,
// and on a fleet with no runtime source attached that is most of the rows --
// throbbing all of them would train the reader to ignore the one row that is
// actually broken. Same rule the compliance model draws elsewhere: a confirmed
// contradiction is an error, an inability to look is not.
func needsAttention(s string) bool { return statusPresentationFor(s).Rank <= 1 }

// dimColor scales c's brightness by k, clamped to [0,1]. It is how a pulse is
// drawn: one colour, modulated, rather than a second palette entry per state.
// Scaling rather than blending towards a background colour, because this
// package does not know the terminal's background and guessing it wrong turns a
// throb into a flicker.
func dimColor(c color.Color, k float64) color.Color {
	k = clamp01(k)
	r, g, b, a := c.RGBA()
	// RGBA returns 16-bit premultiplied components; >>8 takes the 8-bit form.
	scale := func(v uint32) uint8 { return uint8(float64(v>>8) * k) }
	return color.RGBA{R: scale(r), G: scale(g), B: scale(b), A: uint8(a >> 8)}
}

// pulseStyle is the style s renders in on the given frame: full brightness at
// the top of the beat, down to pulseFloor at the bottom. A status that is not a
// confirmed problem does not pulse at all, and neither does anything when
// animation is off.
func pulseStyle(c *Context, s string) style {
	if !c.Anim || !needsAttention(s) {
		return statusStyle(s)
	}
	const pulseFloor = 0.45
	k := pulseFloor + (1-pulseFloor)*pulse(c.Frame, pulsePeriod)
	return style{lipgloss.NewStyle().Foreground(dimColor(statusPresentationFor(s).Color, k))}
}
