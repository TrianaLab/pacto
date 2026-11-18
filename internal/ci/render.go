package ci

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
)

// Text writes the plan as the terminal reads it: the detected values, one
// table row per service, then skips, errors and row notes.
func Text(w io.Writer, p *Plan) {
	reg := p.Registry.String()
	if p.Registry.Value == "" {
		reg = "none (pass --registry)"
	}
	_, _ = fmt.Fprintf(w, "Registry: %s\nBase: %s\n", reg, p.Base)
	if p.OIDC {
		_, _ = fmt.Fprintln(w, "OIDC: available, not requested in plan mode")
	}
	if len(p.Snapshot.Sources) == 0 {
		_, _ = fmt.Fprintln(w, "Consumer source: none (IMPACT reads unverified)")
	}
	for _, s := range p.Snapshot.Sources {
		_, _ = fmt.Fprintf(w, "Consumer source: %s (%s, asOf %s)\n", s.Name, s.Status, p.Snapshot.AsOf.UTC().Format("2006-01-02T15:04:05Z"))
	}
	for _, n := range p.Notes {
		_, _ = fmt.Fprintln(w, n)
	}
	_, _ = fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SERVICE\tCHANGED\tVERSION\tCLASSIFICATION\tIMPACT\tINFERRED")
	for _, r := range p.Rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", cell(r.Service), cell(r.Changed), version(r), cell(r.Classification), cell(r.Impact.Text), inferred(r.Inferred))
	}
	_ = tw.Flush()
	for _, s := range p.Skipped {
		_, _ = fmt.Fprintf(w, "skipped: %s (from %s)\n", s.Rel, s.Rule)
	}
	for _, r := range p.Rows {
		for _, e := range r.Errors {
			_, _ = fmt.Fprintf(w, "error: %s: %s\n", r.Dir, e)
		}
		for _, n := range r.Notes {
			_, _ = fmt.Fprintf(w, "%s: %s\n", cell(r.Service), n)
		}
	}
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
