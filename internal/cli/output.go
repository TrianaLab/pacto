package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/diff"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/sbom"
)

// formatResult dispatches between JSON, markdown and text output.
// When markdownFn is nil and format is "markdown", it falls back to textFn.
func formatResult(cmd *cobra.Command, format string, result any, textFn, markdownFn func() error) error {
	switch format {
	case "json":
		return printJSON(cmd, result)
	case "markdown":
		if markdownFn != nil {
			return markdownFn()
		}
		return textFn()
	default:
		return textFn()
	}
}

func printInitResult(cmd *cobra.Command, result *app.InitResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		revealInit(cmd, cmd.OutOrStdout(), initLines(result))
		return nil
	}, nil)
}

func printValidateResult(cmd *cobra.Command, result *app.ValidateResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		if result.Valid {
			if useColor(w) {
				_, _ = fmt.Fprintf(w, "%s %s is valid\n", checkGlyph(true), result.Path)
			} else {
				_, _ = fmt.Fprintf(w, "%s is valid\n", result.Path)
			}
		} else {
			if useColor(w) {
				_, _ = fmt.Fprintf(w, "%s %s is invalid\n", crossGlyph(true), result.Path)
			} else {
				_, _ = fmt.Fprintf(w, "%s is invalid\n", result.Path)
			}
		}

		for _, e := range result.Errors {
			_, _ = fmt.Fprintf(cmd.OutOrStderr(), "  ERROR [%s] %s: %s\n", e.Code, e.Path, e.Message)
		}

		for _, w := range result.Warnings {
			_, _ = fmt.Fprintf(cmd.OutOrStderr(), "  WARN  [%s] %s: %s\n", w.Code, w.Path, w.Message)
		}

		return nil
	}, nil)
}

func printPackResult(cmd *cobra.Command, result *app.PackResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%sPacked %s@%s -> %s\n", okGlyph(w), result.Name, result.Version, result.Output)
		return nil
	}, nil)
}

func printPushResult(cmd *cobra.Command, result *app.PushResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%sPushed %s@%s -> %s\n", okGlyph(w), result.Name, result.Version, result.Ref)
		_, _ = fmt.Fprintf(w, "Digest: %s\n", result.Digest)
		return nil
	}, nil)
}

func printPullResult(cmd *cobra.Command, result *app.PullResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%sPulled %s@%s -> %s/\n", okGlyph(w), result.Name, result.Version, result.Output)
		return nil
	}, nil)
}

func printDiffResult(cmd *cobra.Command, result *app.DiffResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		rendered := graph.RenderDiffTreeColored(result.GraphDiff, diffColors(w))
		hasSBOM := result.SBOMDiff != nil && len(result.SBOMDiff.Changes) > 0
		hasDepSBOM := hasAnyDepSBOM(result.DependencyDiffs)

		_, _ = fmt.Fprintf(w, "Classification: %s\n", colorClassification(w, result.Classification))

		if len(result.Changes) == 0 && len(result.DependencyDiffs) == 0 && rendered == "" && !hasSBOM && !hasDepSBOM {
			_, _ = fmt.Fprintln(w, "No changes detected.")
			return nil
		}

		if len(result.Changes) > 0 {
			_, _ = fmt.Fprintf(w, "Changes (%d):\n", len(result.Changes))
			for _, c := range result.Changes {
				_, _ = fmt.Fprintf(w, "  [%s] %s (%s): %s%s\n",
					c.Classification, c.Path, c.Type, c.Reason, formatChangeValues(c))
			}
		}
		for _, dd := range result.DependencyDiffs {
			_, _ = fmt.Fprintf(w, "\nDependency %s [%s] (%d):\n", dd.Name, dd.Classification, len(dd.Changes))
			for _, c := range dd.Changes {
				_, _ = fmt.Fprintf(w, "  [%s] %s (%s): %s%s\n",
					c.Classification, c.Path, c.Type, c.Reason, formatChangeValues(c))
			}
			if dd.SBOMDiff != nil && len(dd.SBOMDiff.Changes) > 0 {
				printSBOMDiff(w, dd.SBOMDiff)
			}
		}

		if rendered != "" {
			_, _ = fmt.Fprintf(w, "\nDependency graph changes:\n%s", rendered)
		}

		if hasSBOM {
			printSBOMDiff(w, result.SBOMDiff)
		}

		return nil
	}, func() error {
		return printDiffMarkdown(cmd, result)
	})
}

func printDiffMarkdown(cmd *cobra.Command, result *app.DiffResult) error {
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(w, "## Contract Diff\n\n**Classification:** `%s`\n\n", result.Classification)

	hasChanges := len(result.Changes) > 0 || len(result.DependencyDiffs) > 0
	rendered := graph.RenderDiffTree(result.GraphDiff)
	hasSBOM := result.SBOMDiff != nil && len(result.SBOMDiff.Changes) > 0
	hasDepSBOM := hasAnyDepSBOM(result.DependencyDiffs)

	if !hasChanges && rendered == "" && !hasSBOM && !hasDepSBOM {
		_, _ = fmt.Fprintln(w, "No changes detected.")
		return nil
	}

	if len(result.Changes) > 0 {
		_, _ = fmt.Fprintf(w, "### Changes (%d)\n\n", len(result.Changes))
		printDiffMarkdownTable(w, result.Changes)
	}

	for _, dd := range result.DependencyDiffs {
		_, _ = fmt.Fprintf(w, "### Dependency: %s (`%s`)\n\n", dd.Name, dd.Classification)
		if len(dd.Changes) > 0 {
			printDiffMarkdownTable(w, dd.Changes)
		}
		if dd.SBOMDiff != nil && len(dd.SBOMDiff.Changes) > 0 {
			printSBOMMarkdownTable(w, dd.Name, dd.SBOMDiff)
		}
	}

	if rendered != "" {
		_, _ = fmt.Fprintf(w, "### Dependency Graph Changes\n\n```\n%s```\n", rendered)
	}

	if hasSBOM {
		_, _ = fmt.Fprintf(w, "### SBOM Changes\n\n")
		printSBOMChangeRows(w, result.SBOMDiff.Changes)
	}

	return nil
}

func printGraphResult(cmd *cobra.Command, result *app.GraphResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprint(w, graph.RenderTreeColored(result, treeColors(w)))
		return nil
	}, nil)
}

// treeColors returns ANSI colorizers when color is enabled for w, else the
// zero value (plain output).
func treeColors(w io.Writer) graph.TreeColors {
	if !useColor(w) {
		return graph.TreeColors{}
	}
	ansi := func(code string) func(string) string {
		return func(s string) string { return "\033[" + code + "m" + s + "\033[0m" }
	}
	return graph.TreeColors{
		Name:    ansi("1"),  // bold
		Version: ansi("36"), // cyan
		Marker:  ansi("2"),  // dim
		Error:   ansi("31"), // red
		Warn:    ansi("33"), // yellow
	}
}

// diffColors returns ANSI colorizers for diff trees when color is enabled for w.
func diffColors(w io.Writer) graph.DiffColors {
	if !useColor(w) {
		return graph.DiffColors{}
	}
	ansi := func(code string) func(string) string {
		return func(s string) string { return "\033[" + code + "m" + s + "\033[0m" }
	}
	return graph.DiffColors{Added: ansi("32"), Removed: ansi("31"), Changed: ansi("33")}
}

// colorClassification colors the diff classification string; passthrough off-TTY.
func colorClassification(w io.Writer, s string) string {
	if !useColor(w) {
		return s
	}
	switch s {
	case "BREAKING":
		return crossGlyph(true) + " \033[31m" + s + "\033[0m"
	case "NON_BREAKING":
		return checkGlyph(true) + " \033[32m" + s + "\033[0m"
	case "POTENTIAL_BREAKING":
		return "\033[33m" + s + "\033[0m"
	default:
		return s
	}
}

func printExplainResult(cmd *cobra.Command, result *app.ExplainResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "Service: %s@%s\n", result.Name, result.Version)
		if !result.Owner.IsEmpty() {
			_, _ = fmt.Fprintf(w, "Owner: %s\n", result.Owner.DisplayString())
		}
		_, _ = fmt.Fprintf(w, "Pacto Version: %s\n", result.PactoVersion)

		if result.Workload != "" {
			_, _ = fmt.Fprintf(w, "\nWorkload: %s\n", result.Workload)
		}

		if result.State != nil {
			_, _ = fmt.Fprintf(w, "\nState:\n")
			_, _ = fmt.Fprintf(w, "  Type: %s\n", result.State.Type)
			_, _ = fmt.Fprintf(w, "  Persistence: %s/%s\n", result.State.Scope, result.State.Durability)
			_, _ = fmt.Fprintf(w, "  Data Criticality: %s\n", result.State.DataCriticality)
		}

		if len(result.Capabilities) > 0 {
			_, _ = fmt.Fprintf(w, "\nCapabilities (%d):\n", len(result.Capabilities))
			for _, cap := range result.Capabilities {
				if cap.Ref != "" {
					_, _ = fmt.Fprintf(w, "  - %s: %s\n", cap.Type, cap.Ref)
				} else {
					_, _ = fmt.Fprintf(w, "  - %s\n", cap.Type)
				}
			}
		}

		if len(result.Interfaces) > 0 {
			_, _ = fmt.Fprintf(w, "\nInterfaces (%d):\n", len(result.Interfaces))
			for _, iface := range result.Interfaces {
				_, _ = fmt.Fprintf(w, "  - %s (%s: %s", iface.Name, iface.Type, iface.Ref)
				if iface.Visibility != "" {
					_, _ = fmt.Fprintf(w, ", %s", iface.Visibility)
				}
				_, _ = fmt.Fprintln(w, ")")
			}
		}

		if len(result.Dependencies) > 0 {
			_, _ = fmt.Fprintf(w, "\nDependencies (%d):\n", len(result.Dependencies))
			for _, dep := range result.Dependencies {
				req := "optional"
				if dep.Required {
					req = "required"
				}
				_, _ = fmt.Fprintf(w, "  - %s: %s (%s, %s)\n", dep.Name, dep.Ref, dep.Compatibility, req)
			}
		}

		if result.Readiness != nil {
			printReadiness(w, result.Readiness)
		}

		return nil
	}, nil)
}

// printReadiness renders the readiness summary, per-check table and revision
// history for the text output of `pacto explain`.
func printReadiness(w io.Writer, r *app.ExplainReadiness) {
	gate := "PASS"
	if !r.Passing {
		gate = "FAIL"
	}
	_, _ = fmt.Fprintf(w, "\nReadiness:\n")
	_, _ = fmt.Fprintf(w, "  Score: %d\n", r.Score)
	_, _ = fmt.Fprintf(w, "  Gate: %s (score %d / minScore %d)\n", gate, r.Score, r.MinScore)
	_, _ = fmt.Fprintf(w, "  Earned Weight: %d\n", r.EarnedWeight)
	_, _ = fmt.Fprintf(w, "  Total Weight: %d\n", r.TotalWeight)
	if r.Expired {
		_, _ = fmt.Fprintf(w, "  Expires: %s (EXPIRED)\n", r.Expires)
	} else if r.DaysRemaining != nil {
		_, _ = fmt.Fprintf(w, "  Expires: %s (%d days remaining)\n", r.Expires, *r.DaysRemaining)
	} else {
		_, _ = fmt.Fprintf(w, "  Expires: %s\n", r.Expires)
	}
	_, _ = fmt.Fprintf(w, "  Status: %d done, %d partial, %d not-done, %d deferred\n",
		r.DoneCount, r.PartialCount, r.NotDoneCount, r.DeferredCount)

	_, _ = fmt.Fprintf(w, "\n  Checks:\n")
	for _, ch := range r.Checks {
		category := ch.Category
		if category == "" {
			category = "—"
		}
		line := fmt.Sprintf("    - %-16s %-14s %-9s weight=%d earned=%d",
			ch.ID, category, ch.Status, ch.Weight, ch.EarnedWeight)
		if ch.Excluded {
			line += " (excluded)"
		}
		if ch.Evidence != "" {
			line += " evidence=" + ch.Evidence
		}
		_, _ = fmt.Fprintln(w, line)
	}

	if len(r.Revisions) > 0 {
		_, _ = fmt.Fprintf(w, "\n  Revision History:\n")
		for _, rev := range r.Revisions {
			_, _ = fmt.Fprintf(w, "    - %s  v%s  %s: %s\n", rev.Date, rev.Version, rev.Author, rev.Description)
		}
	}
}

func printGenerateResult(cmd *cobra.Command, result *app.GenerateResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "Generated %d file(s) using %s\n", result.FilesCount, result.Plugin)
		_, _ = fmt.Fprintf(w, "Output: %s/\n", result.OutputDir)
		if result.Message != "" {
			_, _ = fmt.Fprintf(w, "Message: %s\n", result.Message)
		}
		return nil
	}, nil)
}

func printDocResult(cmd *cobra.Command, result *app.DocResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), result.Markdown)
		if result.Path != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStderr(), "Wrote %s\n", result.Path)
		}
		return nil
	}, nil)
}

func printSBOMDiff(w io.Writer, result *sbom.Result) {
	_, _ = fmt.Fprintf(w, "\nSBOM changes (%d):\n", len(result.Changes))
	for _, c := range result.Changes {
		switch c.Type {
		case sbom.PackageAdded:
			_, _ = fmt.Fprintf(w, "  + %s@%s\n", c.Package, c.NewValue)
		case sbom.PackageRemoved:
			_, _ = fmt.Fprintf(w, "  - %s@%s\n", c.Package, c.OldValue)
		case sbom.PackageModified:
			_, _ = fmt.Fprintf(w, "  ~ %s %s: %s -> %s\n", c.Package, c.Field, c.OldValue, c.NewValue)
		}
	}
}

func printDiffMarkdownTable(w io.Writer, changes []diff.Change) {
	_, _ = fmt.Fprintln(w, "| Classification | Path | Type | Reason | Old | New |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---|")
	for _, c := range changes {
		_, _ = fmt.Fprintf(w, "| %s | `%s` | %s | %s | %s | %s |\n",
			c.Classification, c.Path, c.Type, c.Reason,
			formatMDValue(c.OldValue), formatMDValue(c.NewValue))
	}
	_, _ = fmt.Fprintln(w)
}

func formatMDValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("`%v`", v)
}

func formatChangeValues(c diff.Change) string {
	switch c.Type {
	case diff.Modified:
		if c.OldValue != nil && c.NewValue != nil {
			return fmt.Sprintf(" [%v -> %v]", c.OldValue, c.NewValue)
		}
	case diff.Added:
		if c.NewValue != nil {
			return fmt.Sprintf(" [+ %v]", c.NewValue)
		}
	case diff.Removed:
		if c.OldValue != nil {
			return fmt.Sprintf(" [- %v]", c.OldValue)
		}
	}
	return ""
}

func nonEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func hasAnyDepSBOM(deps []app.DependencyDiff) bool {
	for _, dd := range deps {
		if dd.SBOMDiff != nil && len(dd.SBOMDiff.Changes) > 0 {
			return true
		}
	}
	return false
}

func printSBOMMarkdownTable(w io.Writer, name string, result *sbom.Result) {
	_, _ = fmt.Fprintf(w, "#### SBOM Changes: %s\n\n", name)
	printSBOMChangeRows(w, result.Changes)
}

// printSBOMChangeRows renders the shared SBOM markdown table (header + rows).
func printSBOMChangeRows(w io.Writer, changes []sbom.Change) {
	_, _ = fmt.Fprintln(w, "| Package | Type | Field | Old | New |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, c := range changes {
		_, _ = fmt.Fprintf(w, "| `%s` | %s | %s | %s | %s |\n",
			c.Package, c.Type, c.Field,
			formatMDValue(nonEmpty(c.OldValue)), formatMDValue(nonEmpty(c.NewValue)))
	}
	_, _ = fmt.Fprintln(w)
}

func printLockResult(cmd *cobra.Command, r *app.LockResult, format string) error {
	return formatResult(cmd, format, r, func() error {
		w := cmd.OutOrStdout()
		if r.UpToDate {
			_, _ = fmt.Fprintf(w, "%spacto.lock is up to date (%d dependencies, %d references)\n", okGlyph(w), r.Dependencies, r.References)
			return nil
		}
		_, _ = fmt.Fprintf(w, "%swrote %s (%d dependencies, %d references)\n", okGlyph(w), r.Path, r.Dependencies, r.References)
		return nil
	}, nil)
}

func printJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
