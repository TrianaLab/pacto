package ci

import (
	"cmp"
	"fmt"
	"html"
	"io"
	"slices"
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
	if !publishing(p) {
		_, _ = fmt.Fprintln(tw, strings.Join(columns, "\t"))
	}
	for _, r := range p.Rows {
		_, _ = fmt.Fprintln(tw, strings.Join(rowCells(p, r), "\t"))
	}
	_ = tw.Flush()
	for _, l := range advisoryLines(p) {
		_, _ = fmt.Fprintln(w, l)
	}
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
	if publishing(p) {
		b.WriteString("### pacto ci publish\n\n")
		for _, l := range header(p) {
			b.WriteString("- " + code(l) + "\n")
		}
		b.WriteString("\n")
		for _, r := range p.Rows {
			b.WriteString("- " + code(strings.Join(publishCells(r), "  ")) + "\n")
		}
		writeFooter(&b, p)
		return b.String()
	}
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
	writeAdvisories(&b, p)
	writeFooter(&b, p)
	return b.String()
}

// writeFooter writes the footer lines as code-span bullets.
func writeFooter(b *strings.Builder, p *Plan) {
	if f := footer(p); len(f) > 0 {
		b.WriteString("\n")
		for _, l := range f {
			b.WriteString("- " + code(l) + "\n")
		}
	}
}

// advisoryLines are one line per possible break on an inferred interface.
func advisoryLines(p *Plan) []string {
	var out []string
	for _, r := range p.Rows {
		for _, a := range r.Advisories {
			out = append(out, a.Line(r.Service))
		}
	}
	return out
}

// writeAdvisories writes each advisory as a code-span bullet with the YAML
// that declares its interface in a details block.
func writeAdvisories(b *strings.Builder, p *Plan) {
	for _, r := range p.Rows {
		for _, a := range r.Advisories {
			b.WriteString("\n- " + code(a.Line(r.Service)) + "\n\n  <details><summary>Declare " + html.EscapeString(a.Interface) +
				"</summary>\n\n  ```yaml\n" + indent(a.Declare) + "  ```\n\n  </details>\n")
		}
	}
}

// indent is s with every line indented two spaces, for a list item. A
// fence inside s becomes quotes, so it cannot close the block.
func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		b.WriteString("  " + strings.ReplaceAll(l, "```", "'''") + "\n")
	}
	return b.String()
}

// publishing reports whether p is a publish run: refused, or a row with an
// outcome.
func publishing(p *Plan) bool {
	return p.Refused != "" || slices.ContainsFunc(p.Rows, func(r Row) bool { return r.State != "" })
}

// rowCells are r's cells in p's mode.
func rowCells(p *Plan, r Row) []string {
	if publishing(p) {
		return publishCells(r)
	}
	return cells(r)
}

// publishCells are a publish row's cells: the service, then what happened.
func publishCells(r Row) []string {
	out := []string{cell(r.Service)}
	switch r.State {
	case StatePublished:
		out = append(out, cell(r.Baseline)+" -> "+r.To+attempt(r))
		if r.Tool != "" {
			out = append(out, "built with "+r.Tool)
		}
		if r.Identity != "" {
			out = append(out, "signed "+r.Identity)
		}
		return append(out, "published "+r.Published)
	case StateFailed:
		if strings.HasPrefix(r.Reason, "superseded: ") {
			return append(out, r.Reason+attempt(r))
		}
	case StateUnchanged:
		return append(out, StateUnchanged)
	}
	return append(out, r.State+": "+r.Reason+attempt(r))
}

// attempt is " (attempt n/3)" for a row that re-planned, else "".
func attempt(r Row) string {
	if r.Attempts < 2 {
		return ""
	}
	return fmt.Sprintf(" (attempt %d/%d)", r.Attempts, MaxAttempts)
}

// MaxAttempts bounds how often a publish that lost a tag to a concurrent run
// re-plans (D14).
const MaxAttempts = 3

// code is s as one inline code span: backticks become quotes and line
// breaks spaces, so s can neither close the span nor start a new block.
func code(s string) string {
	return "`" + strings.NewReplacer("`", "'", "\r", " ", "\n", " ").Replace(s) + "`"
}

// columns are the plan table's headers.
var columns = []string{"SERVICE", "CHANGED", "VERSION", "CLASSIFICATION", "IMPACT", "INFERRED"}

// cells are r's table cells, "-" for an empty one.
func cells(r Row) []string {
	return []string{cell(r.Service), cell(r.Changed), version(r), cell(r.Classification), cell(r.Impact.Text), inferred(r.Inferred, r.Accepted)}
}

// header are the detected values, each with its source, and the plan notes.
func header(p *Plan) []string {
	reg := p.Registry.String()
	if p.Registry.Value == "" {
		reg = "none (pass --registry)"
	}
	out := []string{"CI: " + platform(p.Platform), "Registry: " + reg, "Base: " + p.Base.String()}
	if p.Refused != "" {
		out = append(out, "refused: "+p.Refused)
	}
	if publishing(p) {
		// No IMPACT column: an unverified impact never stops a publish.
		return append(out, p.Notes...)
	}
	if p.OIDC {
		out = append(out, "OIDC: available, not requested in plan mode")
	}
	switch {
	case len(p.Snapshot.Sources) > 0:
		for _, s := range p.Snapshot.Sources {
			out = append(out, fmt.Sprintf("Consumer source: %s (%s, asOf %s)", s.Name, s.Status, p.Snapshot.AsOf.UTC().Format("2006-01-02T15:04:05Z")))
		}
	case len(p.ConsumerSources) == 0:
		out = append(out, "Consumer source: none (IMPACT reads unverified)")
	default:
		// Named but never read: no row reached impact, so no snapshot was built.
		for _, s := range p.ConsumerSources {
			out = append(out, "Consumer source: "+s+" (not read: no changed service with a baseline)")
		}
	}
	if p.Index != "" {
		out = append(out, "Index: "+p.Index)
	}
	out = append(out, RangesLines(p.Ranges)...)
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
		for _, l := range carriedLines(r) {
			out = append(out, cell(r.Service)+": "+l)
		}
	}
	return out
}

// carriedLines are one line per field a registry decision names for r, then
// where its version comes from; none when it names no field.
func carriedLines(r Row) []string {
	if len(r.Carried) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Carried)+1)
	for _, c := range r.Carried {
		label := strings.TrimPrefix(strings.NewReplacer("[", ".", "]", "").Replace(c.Field), "service.")
		verb := "accepted"
		if c.Field == "service.owner" {
			verb = "declared"
		}
		at := rev(c.Record)
		switch c.State {
		case CarriedOverridden:
			out = append(out, label+": "+c.Value+", from pacto.yaml ("+c.Reason+", rev "+at+")")
		case CarriedDropped:
			out = append(out, label+": "+c.Value+", "+verb+" in registry (rev "+at+"), not carried: "+c.Reason)
		default:
			out = append(out, label+": "+c.Value+", "+verb+" in registry (rev "+at+")")
		}
	}
	switch v := cmp.Or(r.To, r.Next); {
	case v == "":
	case r.Derived && r.BaselineDigest == "":
		out = append(out, "service.version: "+v+", derived from "+cell(r.Baseline))
	case r.Derived:
		out = append(out, "service.version: "+v+", derived from "+r.Baseline+" (rev "+rev(r.BaselineDigest)+")")
	default:
		out = append(out, "service.version: "+v+", from pacto.yaml")
	}
	return out
}

// rev is a digest, or a reference ending in one, as sha256:<12 hex>; anything
// else as it is.
func rev(ref string) string {
	_, d, ok := strings.Cut(ref, "sha256:")
	if !ok {
		return ref
	}
	return "sha256:" + d[:min(12, len(d))]
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

// inferred is the INFERRED cell: "<n> fields", "1 field", or "-" for none,
// then ", <a> accepted" when signed acceptances made fields declared.
func inferred(n, accepted int) string {
	out := strconv.Itoa(n) + " fields"
	switch {
	case n == 1:
		out = "1 field"
	case n == 0 && accepted == 0:
		return "-"
	}
	if accepted > 0 {
		out += ", " + strconv.Itoa(accepted) + " accepted"
	}
	return out
}
