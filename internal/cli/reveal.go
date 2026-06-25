package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/trianalab/pacto/v2/internal/app"
)

// revealStagger is a var so tests shrink it to ~1ms. ~300ms total / lines.
var revealStagger = 60 * time.Millisecond

// bannerLines returns the ANSI Shadow PACTO block + tagline, colored when color.
// Colored rows are wrapped in "\033[36m"…"\033[0m" (cyan, matching the old banner
// so the existing banner tests pass). Pure.
func bannerLines(color bool) []string {
	rows := []string{
		"██████╗  █████╗  ██████╗████████╗ ██████╗",
		"██╔══██╗██╔══██╗██╔════╝╚══██╔══╝██╔═══██╗",
		"██████╔╝███████║██║        ██║   ██║   ██║",
		"██╔═══╝ ██╔══██║██║        ██║   ██║   ██║",
		"██║     ██║  ██║╚██████╗   ██║   ╚██████╔╝",
		"╚═╝     ╚═╝  ╚═╝ ╚═════╝   ╚═╝    ╚═════╝",
	}
	if color {
		for i, row := range rows {
			rows[i] = "\033[36m" + row + "\033[0m"
		}
	}
	return append(rows, "  service contracts for cloud-native")
}

// bannerStatic returns the full banner, all rows printed at once. Pure.
func bannerStatic(color bool) string {
	return strings.Join(bannerLines(color), "\n") + "\n"
}

// revealBanner prints the banner one row at a time with revealStagger between
// rows — a top-to-bottom cascade. Each row is printed exactly once (no cumulative
// reprinting), so it draws in cleanly and the final result equals bannerStatic.
// Synchronous: finishes before the help text follows.
func revealBanner(w io.Writer, color bool) {
	for i, line := range bannerLines(color) {
		if i > 0 {
			time.Sleep(revealStagger)
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

// initLines returns the exact 4 current strings, in order. Pure.
func initLines(r *app.InitResult) []string {
	return []string{
		"Created " + r.Dir + "/",
		"  " + r.Path,
		"  " + r.Dir + "/interfaces/",
		"  " + r.Dir + "/configuration/",
	}
}

// revealInit prints lines[0] as-is (header), then lines[1:] each as scaffolded
// items. animate(w) → staggered "  ✓ <path>"; else verbatim (byte-identical).
func revealInit(w io.Writer, lines []string) {
	if len(lines) == 0 {
		return
	}

	// Print header
	_, _ = fmt.Fprintln(w, lines[0])

	if animate(w) {
		// Animated path: stagger items with checkmarks
		for _, item := range lines[1:] {
			time.Sleep(revealStagger)
			trimmed := strings.TrimLeft(item, " ")
			_, _ = fmt.Fprintf(w, "  %s %s\n", checkGlyph(useColor(w)), trimmed)
		}
	} else {
		// Off-gate path: verbatim output
		for _, item := range lines[1:] {
			_, _ = fmt.Fprintln(w, item)
		}
	}
}
