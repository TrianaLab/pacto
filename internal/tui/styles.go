package tui

import "charm.land/lipgloss/v2"

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
