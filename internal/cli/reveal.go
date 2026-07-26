package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v2/internal/app"
)

// revealStagger is a var so tests shrink it to ~1ms. ~300ms total / lines.
var revealStagger = 60 * time.Millisecond

// bannerLines returns the solid-block PACTO wordmark plus a tagline led by the
// brackets mark, colored brand indigo when color (wrapped in ansiIndigo…ansiReset).
// Pure.
func bannerLines(color bool) []string {
	rows := []string{
		"██████   █████   ██████ ████████  ██████",
		"██   ██ ██   ██ ██         ██    ██    ██",
		"██████  ███████ ██         ██    ██    ██",
		"██      ██   ██ ██         ██    ██    ██",
		"██      ██   ██  ██████    ██     ██████",
	}
	if color {
		for i, row := range rows {
			rows[i] = ansiIndigo + row + ansiReset
		}
	}
	mark := "‹•›"
	if color {
		mark = ansiIndigo + mark + ansiReset
	}
	return append(rows, "  "+mark+" service contracts for cloud-native")
}

// bannerStatic returns the full banner, all rows printed at once. Pure.
// The help banner is intentionally static (no animation); the cascade reveal is
// reserved for `pacto init` (revealInit).
func bannerStatic(color bool) string {
	return strings.Join(bannerLines(color), "\n") + "\n"
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
// items. animate → staggered "  ✓ <path>"; else verbatim (byte-identical).
func revealInit(cmd *cobra.Command, w io.Writer, lines []string) {
	if len(lines) == 0 {
		return
	}

	// Print header
	_, _ = fmt.Fprintln(w, lines[0])

	if animate(cmd, w) {
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
