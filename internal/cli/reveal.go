package cli

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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
		return []string{"Nothing to add in " + r.Dir + "/: every file init writes is already there"}
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
		_, _ = fmt.Fprintln(w, "\nNothing to add: every file init writes is already there.")
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
	switch r.Mode {
	case "pactify", "complete", "blank":
		section("Found", r.Found)
		printDiscovered(w, r)
	}
	switch r.Mode {
	case "pactify", "complete":
		section("Depends on", findings(r.Dependencies))
		section("Used by", findings(r.Consumers))
		printExternalSummary(w, r)
	case "blank":
		printExternalSummary(w, r)
	case "template":
		section("Found", r.Found)
	}
	printCreationSteps(w, "\nStatus", r.Steps)
	section("Unknown", notFindings(r))
	section("Next", r.Next)
	var kept []string
	for _, f := range r.Files {
		if f.Action == pactify.Keep {
			kept = append(kept, f.Path)
		}
	}
	section("Already there, left as it is", kept)
}

// printDiscovered prints what Pacto read from the folder and keeps apart
// from pacto.yaml, one row per value with where it was read, then every
// file it recognized but did not read.
func printDiscovered(w io.Writer, r *app.InitResult) {
	rec := r.Inferred
	if rec == nil {
		return
	}
	rows := discoveredRows(rec.Fields)
	if len(rows) > 0 {
		_, _ = fmt.Fprintf(w, "\n%s inferred:\n", r.Service)
		width := 0
		for _, row := range rows {
			width = max(width, len(row[0]))
		}
		for _, row := range rows {
			_, _ = fmt.Fprintf(w, "  %-*s  %s\n", width, row[0], row[1])
		}
	} else if len(rec.Findings) > 0 {
		_, _ = fmt.Fprintln(w)
	}
	for _, f := range rec.Findings {
		_, _ = fmt.Fprintf(w, "finding: %s\n", f.Message)
	}
}

// discoveredRows are a label and what follows it for each field, in the
// record's order: the environments in one row, an owner read from an owner
// file marked attested and every source it outranks named.
func discoveredRows(fields []contract.Inferred) [][2]string {
	var rows [][2]string
	var envs, envSources []string
	for _, f := range fields {
		if f.External != nil { // printExternalSummary lists them
			continue
		}
		suffix := discoveredSuffix(f, "")
		switch {
		case strings.HasPrefix(f.Field, "environments["):
			envs = append(envs, strings.TrimSuffix(strings.TrimPrefix(f.Field, "environments["), "].file"))
			envSources = append(envSources, shortSource(f.Source)+suffix)
		case f.Field == "service.owner":
			rows = append(rows, [2]string{"owner", "from " + shortSource(f.Source) + suffix})
		case strings.HasPrefix(f.Field, "ports["):
			if strings.HasPrefix(path.Base(f.Source), "Dockerfile") {
				suffix = discoveredSuffix(f, "EXPOSE")
			}
			rows = append(rows, [2]string{"port " + strings.TrimSuffix(f.Value, "/tcp"), "from " + shortSource(f.Source) + suffix})
		default:
			rows = append(rows, [2]string{f.Field, f.Value + " from " + shortSource(f.Source) + suffix})
		}
	}
	if len(envs) > 0 {
		rows = append(rows, [2]string{"environments", strings.Join(envs, ", ") + " from " + strings.Join(envSources, ", ")})
	}
	return rows
}

// discoveredSuffix is " (attested, outranks ...)" for f after the note
// lead, or what of it applies, "" when nothing does. Attested only names
// where an owner was read: it routes nothing and clears no gate.
func discoveredSuffix(f contract.Inferred, lead string) string {
	var notes []string
	if lead != "" {
		notes = append(notes, lead)
	}
	if f.Class == contract.InferredClassOwnerFile {
		notes = append(notes, "attested")
	}
	if len(f.Outranks) > 0 {
		short := make([]string, len(f.Outranks))
		for i, o := range f.Outranks {
			short[i] = shortSource(o)
		}
		notes = append(notes, "outranks "+strings.Join(short, ", "))
	}
	if len(notes) == 0 {
		return ""
	}
	return " (" + strings.Join(notes, ", ") + ")"
}

// shortSource is source as init prints it: a file read from the repository
// outside the folder by its base name and line, such as CODEOWNERS:4.
func shortSource(source string) string {
	if rel, ok := strings.CutPrefix(source, "repository:"); ok {
		return path.Base(rel)
	}
	return source
}

// notFindings is r.Unknown less the discovery findings printDiscovered
// already printed; the count of the ones past the record's bound stays.
func notFindings(r *app.InitResult) []string {
	if r.Inferred == nil {
		return r.Unknown
	}
	return slices.DeleteFunc(slices.Clone(r.Unknown), func(u string) bool {
		return slices.ContainsFunc(r.Inferred.Findings, func(f contract.InferredFinding) bool { return f.Message == u })
	})
}

// printExternalSummary prints the plan line for the external dependencies
// the folder names, one line per dependency under it, then the catalogs that
// classified them. A host is printed as host and port only.
func printExternalSummary(w io.Writer, r *app.InitResult) {
	if len(r.ExternalSummary) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\n%s\n", r.ExternalSummary[0])
	for _, l := range r.ExternalSummary[1:] {
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
	_, _ = fmt.Fprintf(w, "  catalog %s\n", r.Catalog)
}

// printCreationSteps prints how far creating from a golden path got under
// title, one line per step: its status, the step and what it rests on.
func printCreationSteps(w io.Writer, title string, steps []fleet.CreationStep) {
	if len(steps) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, title)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, s := range steps {
		step := s.Step
		if s.Name != "" {
			step += " " + s.Name
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\n", s.Status, step, s.Detail)
	}
	_ = tw.Flush()
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
