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
// so the existing banner tests pass). Pure. The static banner is bannerFrame at
// the final step.
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

// bannerFrame returns the banner revealed up to `step` lines (color-swept/cascade).
// step >= len → full static banner. Pure.
func bannerFrame(step int, color bool) string {
	lines := bannerLines(color)
	if step >= len(lines) {
		return strings.Join(lines, "\n") + "\n"
	}
	return strings.Join(lines[:step], "\n") + "\n"
}

// reveal prints frames[0..n] with revealStagger between them, synchronously.
// First and last frame always emitted so a no-sleep test covers the render path.
func reveal(w io.Writer, frame func(step int, color bool) string, steps int, color bool) {
	// Emit first frame
	_, _ = fmt.Fprint(w, frame(0, color))

	// Emit intermediate frames
	for i := 1; i < steps; i++ {
		time.Sleep(revealStagger)
		_, _ = fmt.Fprint(w, frame(i, color))
	}

	// Always emit final frame
	_, _ = fmt.Fprint(w, frame(steps, color))
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
