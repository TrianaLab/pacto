package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// style is a lipgloss style that sanitises whatever it is asked to render.
//
// This is the structural half of the fix that safeText is the mechanical half
// of. Sanitising at each render site was tried three times and enumerated
// wrong three times: the count of sites that touch fleet text is not a number
// anyone can hold, and the sites that were missed -- an err.Error() in the
// output pane's status line, a prompt's question -- looked exactly like the
// ones that were not. Putting the call HERE inverts the default: a render site
// added next month is safe because it reached for a style, which is the one
// thing every line in this package's frame already does.
//
// Ordering is what makes it work. lipgloss wraps the content in its own SGR
// escapes AFTER Render is handed the string, so the colour survives and only
// the data is cleaned. Sanitising the composed frame instead would have to tell
// this package's escapes from a contract's, and cannot.
//
// It does not cover a bare b.WriteString(untrusted) with no style on it. Those
// call safeText themselves, and TestNoScreenLeaksHostileBytes is what keeps
// them honest.
type style struct{ lipgloss.Style }

// Render sanitises every part and then styles it. lipgloss joins multiple parts
// with a space, which this preserves by handing them on unjoined.
func (s style) Render(parts ...string) string {
	clean := make([]string, len(parts))
	for i, p := range parts {
		clean[i] = safeText(p)
	}
	return s.Style.Render(clean...)
}

var (
	headerStyle = style{lipgloss.NewStyle().Bold(true)}
	dimStyle    = style{lipgloss.NewStyle().Faint(true)}
	errorStyle  = style{lipgloss.NewStyle().Foreground(lipgloss.Color("9"))}
	warnStyle   = style{lipgloss.NewStyle().Foreground(lipgloss.Color("11"))}
	okStyle     = style{lipgloss.NewStyle().Foreground(lipgloss.Color("10"))}
	focusStyle  = style{lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)}
)

// statusStyle maps a fleet status string to the style that should render it.
// Unknown values render plain, which is the honest default: an unrecognised
// status is not a pass.
func statusStyle(s string) style {
	switch s {
	case "compliant", "ok", "healthy", "ready":
		return okStyle
	case "non-compliant", "invalid", "error", "failed":
		return errorStyle
	case "unknown", "stale", "degraded":
		return warnStyle
	default:
		return style{lipgloss.NewStyle()}
	}
}

// safeText makes untrusted text safe to put in a frame. Nothing between a
// contract on disk and this package validates content: internal/fleetsrc/
// local.go calls contract.Parse and nothing else, and the k8s source copies
// custom-resource values through verbatim. So a service can be named
// "svc\r\x1b[2Kpacto lock --check ./svc", and bubbletea's cell renderer acts on
// that carriage return and erase-line while it composes the frame -- the
// emitted bytes already carry the lie, so a terminal that filters escapes is
// not a defence. The write confirmation is the surface that matters: it is the
// only gate on push, pull, lock --update and generate, and a gate that can be
// made to show a different command than the one it runs is decorative.
//
// C0 controls are escaped in caret notation rather than dropped. A name with a
// control character in it is news, and dropping would render two entities that
// differ identically. Caret notation does not extend past DEL, so DEL, the C1
// range and the bidi overrides get a hex escape instead.
func safeText(s string) string {
	if !strings.ContainsFunc(s, isUnsafe) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case !isUnsafe(r):
			b.WriteRune(r)
		case r < 0x20:
			// C0 caret notation: the control's code plus 0x40 is the letter it is
			// named after, so ESC (0x1b) prints as ^[ and CR (0x0d) as ^M.
			b.WriteRune('^')
			b.WriteRune(r + 0x40)
		case r == 0x7f:
			b.WriteString("^?")
		case r > 0xff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\x%02x`, r)
		}
	}
	return b.String()
}

// isUnsafe reports whether r is a character the terminal acts on rather than
// shows. ESC is 0x1b, so the C0 range covers the 7-bit introducers; the C1
// range 0x80-0x9f is the 8-bit form of the same thing, and charmbracelet/x/ansi
// -- the parser inside the renderer that composes the frame, which is where the
// corruption happens -- reads 0x9b as CSI and 0x9d as OSC. No service name,
// owner or ref has a legitimate C1 in it, so over-escaping costs nothing.
//
// The bidi ranges are the same argument one level up. U+202A-U+202E are the
// legacy embedding and override codes and U+2066-U+2069 the isolates; a
// terminal implementing the Unicode bidi algorithm reorders the run after a
// right-to-left override, so "pacto push evil" can be made to DISPLAY as
// something else while the argv underneath is unchanged. That is the same
// forgery the C0 case is about, committed by a character the C0 case does not
// see. Whether a given terminal really reorders it is not worth establishing
// per emulator: no service name, owner or ref has a legitimate use for one, so
// escaping them costs a display nobody wants and closes a class nobody has to
// re-audit.
func isUnsafe(r rune) bool {
	switch {
	case r < 0x20, r >= 0x7f && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}
