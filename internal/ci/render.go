package ci

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Text writes the plan as the terminal reads it: the detected values, one
// table row per service, then skips, errors and row notes.
func Text(w io.Writer, p *Plan) {
	for _, l := range header(p) {
		_, _ = fmt.Fprintln(w, l)
	}
	_, _ = fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, strings.Join(columns, "\t"))
	for _, r := range p.Rows {
		_, _ = fmt.Fprintln(tw, strings.Join(cells(r), "\t"))
	}
	_ = tw.Flush()
	for _, l := range footer(p) {
		_, _ = fmt.Fprintln(w, l)
	}
}

// Markdown is the plan as a step summary or a pacto-plan.md artifact reads
// it. Every value a pull request can shape is a code span, so a service
// name, a path or an error renders as literal text and never as a link, an
// image, a heading or an extra table cell.
func Markdown(p *Plan) string {
	var b strings.Builder
	b.WriteString("### pacto ci plan\n\n")
	for _, l := range header(p) {
		b.WriteString("- " + code(l) + "\n")
	}
	b.WriteString("\n| " + strings.Join(columns, " | ") + " |\n|" + strings.Repeat("---|", len(columns)) + "\n")
	for _, r := range p.Rows {
		cs := cells(r)
		for i, c := range cs {
			if c != "-" {
				cs[i] = strings.ReplaceAll(code(c), "|", `\|`)
			}
		}
		b.WriteString("| " + strings.Join(cs, " | ") + " |\n")
	}
	if f := footer(p); len(f) > 0 {
		b.WriteString("\n")
		for _, l := range f {
			b.WriteString("- " + code(l) + "\n")
		}
	}
	return b.String()
}

// code is s as one inline code span: backticks become quotes and line
// breaks spaces, so s can neither close the span nor start a new block.
func code(s string) string {
	return "`" + strings.NewReplacer("`", "'", "\r", " ", "\n", " ").Replace(s) + "`"
}

// columns are the plan table's headers.
var columns = []string{"SERVICE", "CHANGED", "VERSION", "CLASSIFICATION", "IMPACT", "INFERRED"}

// cells are r's table cells, "-" for an empty one.
func cells(r Row) []string {
	return []string{cell(r.Service), cell(r.Changed), version(r), cell(r.Classification), cell(r.Impact.Text), inferred(r.Inferred)}
}

// header are the detected values, each with its source, and the plan notes.
func header(p *Plan) []string {
	reg := p.Registry.String()
	if p.Registry.Value == "" {
		reg = "none (pass --registry)"
	}
	out := []string{"CI: " + platform(p.Platform), "Registry: " + reg, "Base: " + p.Base.String()}
	if p.OIDC {
		out = append(out, "OIDC: available, not requested in plan mode")
	}
	if len(p.Snapshot.Sources) == 0 {
		out = append(out, "Consumer source: none (IMPACT reads unverified)")
	}
	for _, s := range p.Snapshot.Sources {
		out = append(out, fmt.Sprintf("Consumer source: %s (%s, asOf %s)", s.Name, s.Status, p.Snapshot.AsOf.UTC().Format("2006-01-02T15:04:05Z")))
	}
	return append(out, p.Notes...)
}

// platform is the CI platform line with the variable that detected it.
func platform(name string) string {
	switch name {
	case "github":
		return "github (from GITHUB_ACTIONS)"
	case "gitlab":
		return "gitlab (from GITLAB_CI)"
	}
	return "none"
}

// footer are the skipped folders, then each row's errors and notes.
func footer(p *Plan) []string {
	var out []string
	for _, s := range p.Skipped {
		out = append(out, "skipped: "+s.Rel+" (from "+s.Rule+")")
	}
	for _, r := range p.Rows {
		for _, e := range r.Errors {
			out = append(out, "error: "+r.Dir+": "+e)
		}
		for _, n := range r.Notes {
			out = append(out, cell(r.Service)+": "+n)
		}
	}
	return out
}

// cell is s, or "-" for an empty cell.
func cell(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// version is the VERSION cell: "<baseline> -> <next>", "- -> <next>" for a
// first publish, "-" when nothing is published.
func version(r Row) string {
	if r.Next == "" {
		return "-"
	}
	v := cell(r.Baseline) + " -> " + r.Next
	if r.Derived {
		v += " (derived)"
	}
	return v
}

// inferred is the INFERRED cell: "<n> fields", or "-" for none.
func inferred(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n) + " fields"
}
