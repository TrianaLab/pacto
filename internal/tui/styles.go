package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	focusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
)

// statusStyle maps a fleet status string to the style that should render it.
// Unknown values render plain, which is the honest default: an unrecognised
// status is not a pass.
func statusStyle(s string) lipgloss.Style {
	switch s {
	case "compliant", "ok", "healthy", "ready":
		return okStyle
	case "non-compliant", "invalid", "error", "failed":
		return errorStyle
	case "unknown", "stale", "degraded":
		return warnStyle
	default:
		return lipgloss.NewStyle()
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
// differ identically. Caret notation does not extend past DEL, so DEL and the
// C1 range get a hex escape instead.
func safeText(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case !isControl(r):
			b.WriteRune(r)
		case r < 0x20:
			// C0 caret notation: the control's code plus 0x40 is the letter it is
			// named after, so ESC (0x1b) prints as ^[ and CR (0x0d) as ^M.
			b.WriteRune('^')
			b.WriteRune(r + 0x40)
		case r == 0x7f:
			b.WriteString("^?")
		default:
			fmt.Fprintf(&b, `\x%02x`, r)
		}
	}
	return b.String()
}

// isControl reports whether r is a character the terminal acts on rather than
// shows. ESC is 0x1b, so the C0 range covers the 7-bit introducers; the C1
// range 0x80-0x9f is the 8-bit form of the same thing, and charmbracelet/x/ansi
// -- the parser inside the renderer that composes the frame, which is where the
// corruption happens -- reads 0x9b as CSI and 0x9d as OSC. No service name,
// owner or ref has a legitimate C1 in it, so over-escaping costs nothing.
func isControl(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) }
