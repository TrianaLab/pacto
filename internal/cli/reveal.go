package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/pactify"
)

// revealStagger is the pause between revealed lines: ~300ms in total.
const revealStagger = 60 * time.Millisecond

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

// initLines is the header and one line per file init added. Pure.
func initLines(r *app.InitResult) []string {
	var lines []string
	for _, f := range r.Files {
		switch f.Action {
		case pactify.Add:
			lines = append(lines, "  "+filepath.Join(r.Dir, f.Path))
		case pactify.Append:
			lines = append(lines, "  "+filepath.Join(r.Dir, f.Path)+" (lines added)")
		}
	}
	if len(lines) == 0 {
		return []string{"Nothing to add in " + r.Dir + "/: it is all there"}
	}
	return append([]string{"Created " + r.Dir + "/"}, lines...)
}

// printInitPlan prints what init would add, content included. Pure output.
func printInitPlan(w io.Writer, r *app.InitResult) {
	_, _ = fmt.Fprintf(w, "Plan for %s in %s/ (nothing changed)\n", r.Service, r.Dir)
	printInitFindings(w, r)
	var add []string
	for _, f := range r.Files {
		if f.Action == pactify.Keep {
			continue
		}
		head := "  + " + f.Path
		if f.Action == pactify.Append {
			head += " (adds these lines)"
		}
		add = append(add, head)
		for _, l := range strings.Split(strings.TrimSuffix(f.Content, "\n"), "\n") {
			add = append(add, strings.TrimRight("      "+l, " "))
		}
	}
	if len(add) == 0 {
		_, _ = fmt.Fprintln(w, "\nNothing to add: it is all there.")
		return
	}
	_, _ = fmt.Fprintf(w, "\nWould add\n%s\n\nRun it again without --plan to add these.\n", strings.Join(add, "\n"))
}

// printInitFindings prints the first value: what was found, who depends on
// whom, what is Unknown and what is left to do.
func printInitFindings(w io.Writer, r *app.InitResult) {
	section := func(title string, lines []string) {
		if len(lines) > 0 {
			_, _ = fmt.Fprintf(w, "\n%s\n  - %s\n", title, strings.Join(lines, "\n  - "))
		}
	}
	findings := func(fs []pactify.Finding) []string {
		out := []string{"none found"}
		if len(fs) > 0 {
			out = out[:0]
		}
		for _, f := range fs {
			out = append(out, f.Name+" ("+f.Where+")")
		}
		return out
	}
	if r.Mode == "pactify" {
		section("Found", r.Found)
		section("Depends on", findings(r.Dependencies))
		section("Used by", findings(r.Consumers))
	}
	section("Unknown", r.Unknown)
	section("Next", r.Next)
	var kept []string
	for _, f := range r.Files {
		if f.Action == pactify.Keep {
			kept = append(kept, f.Path)
		}
	}
	section("Already there, left as it is", kept)
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
