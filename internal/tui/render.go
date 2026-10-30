package tui

import (
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/sbom"
)

// renderValidate reports the verdict from the result, never from a returned
// error: app.Validate returns (result, nil) even for a contract that will not
// parse, recording the failure as a PARSE_ERROR entry in result.Errors.
func renderValidate(r *app.ValidateResult) string {
	if r == nil {
		return "(no result)"
	}
	var b strings.Builder
	if r.Valid {
		b.WriteString(okStyle.Render("valid") + "  " + safeText(r.Path) + "\n")
	} else {
		b.WriteString(errorStyle.Render("invalid") + "  " + safeText(r.Path) + "\n")
	}
	for _, e := range r.Errors {
		fmt.Fprintf(&b, "  %s %s %s\n", errorStyle.Render("error"), safeText(e.Code), safeText(e.Message))
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "  %s %s %s\n", warnStyle.Render("warn"), safeText(w.Code), safeText(w.Message))
	}
	return b.String()
}

// renderDiff reports the classification and changes. The CLI fails on a
// BREAKING classification, so the TUI must lead with it styled.
func renderDiff(r *app.DiffResult) string {
	if r == nil {
		return "(no result)"
	}
	var b strings.Builder
	if r.Classification == "BREAKING" {
		b.WriteString(errorStyle.Render("BREAKING") + "  " + safeText(r.OldPath) + " -> " + safeText(r.NewPath) + "\n")
	} else {
		b.WriteString(safeText(r.Classification) + "  " + safeText(r.OldPath) + " -> " + safeText(r.NewPath) + "\n")
	}
	// Not sanitised: the tree arrives already coloured, so safeText would print
	// pkg/graph's own escapes as text. It is sanitised one level down instead, in
	// diffColors — every label RenderDiffTreeColored prints is picked from one of
	// those four functions, so that is the only place that can tell a contract's
	// bytes from lipgloss's.
	graphChanges := graph.RenderDiffTreeColored(r.GraphDiff, diffColors())
	// Every channel a diff can carry a change on, the way internal/cli/output.go
	// tests them. Asking len(Changes) alone was the defect: internal/app/diff.go
	// raises the overall classification off a dependency, so a root with no change
	// of its own and one breaking dependency printed "No changes detected." under
	// a BREAKING header, then returned before reaching the dependency, the graph
	// or the SBOM. Dependency SBOM changes need no condition of their own —
	// internal/app/diff.go only records a DependencyDiff that has changes or SBOM
	// changes, so one always brings its entry with it.
	if len(r.Changes) == 0 && len(r.DependencyDiffs) == 0 && graphChanges == "" && sbomChangeCount(r.SBOMDiff) == 0 {
		b.WriteString("\nNo changes detected.\n")
		return b.String()
	}
	if len(r.Changes) > 0 {
		section(&b, "Changes")
		for _, c := range r.Changes {
			fmt.Fprintf(&b, "  [%s] %s (%s): %s\n", c.Classification, safeText(c.Path), c.Type, safeText(c.Reason))
		}
	}
	if len(r.DependencyDiffs) > 0 {
		section(&b, "Dependency changes")
		for _, dd := range r.DependencyDiffs {
			// Both counts unconditionally: a dependency whose only change is in its
			// SBOM is recorded with an empty Changes slice, and "(0 changes)" beside
			// it is the same lie the header used to tell.
			fmt.Fprintf(&b, "  %s [%s] (%d changes, %d SBOM changes)\n",
				safeText(dd.Name), safeText(dd.Classification), len(dd.Changes), sbomChangeCount(dd.SBOMDiff))
		}
	}
	if graphChanges != "" {
		section(&b, "Graph changes")
		b.WriteString(graphChanges)
	}
	if sbomChangeCount(r.SBOMDiff) > 0 {
		renderSBOMChanges(&b, r.SBOMDiff)
	}
	return b.String()
}

// sbomChangeCount is how many package changes an SBOM diff carries. pkg/sbom
// returns a nil Result when neither side had an SBOM and an empty one when both
// did and agreed, and neither is a change.
func sbomChangeCount(r *sbom.Result) int {
	if r == nil {
		return 0
	}
	return len(r.Changes)
}

// renderSBOMChanges lists package changes in the same three shapes
// internal/cli/output.go prints, so the pane and `pacto diff` do not describe
// one dependency bump two ways. Modified is the default arm rather than a third
// case: pkg/sbom has exactly three change types, and a fourth arm for the one
// that cannot happen is a branch no input reaches.
func renderSBOMChanges(b *strings.Builder, r *sbom.Result) {
	section(b, fmt.Sprintf("SBOM changes (%d)", len(r.Changes)))
	for _, c := range r.Changes {
		switch c.Type {
		case sbom.PackageAdded:
			fmt.Fprintf(b, "  + %s@%s\n", safeText(c.Package), safeText(c.NewValue))
		case sbom.PackageRemoved:
			fmt.Fprintf(b, "  - %s@%s\n", safeText(c.Package), safeText(c.OldValue))
		default:
			fmt.Fprintf(b, "  ~ %s %s: %s -> %s\n",
				safeText(c.Package), safeText(c.Field), safeText(c.OldValue), safeText(c.NewValue))
		}
	}
}

// lifecycleText is the ", deprecated ..." tail of a deprecated interface, or
// nothing.
func lifecycleText(l *contract.InterfaceLifecycle) string {
	if l == nil {
		return ""
	}
	return ", " + l.String()
}

// renderExplain reports the contract's structure and metadata.
func renderExplain(r *app.ExplainResult) string {
	if r == nil {
		return "(no result)"
	}
	var b strings.Builder
	field(&b, "service", r.Name+"@"+r.Version)
	if !r.Owner.IsEmpty() {
		field(&b, "owner", r.Owner.DisplayString())
	}
	field(&b, "pacto version", r.PactoVersion)
	field(&b, "workload", r.Workload)
	if r.State != nil {
		section(&b, "State")
		field(&b, "type", r.State.Type)
		field(&b, "scope", r.State.Scope)
		field(&b, "durability", r.State.Durability)
		field(&b, "data criticality", r.State.DataCriticality)
	}
	if len(r.Capabilities) > 0 {
		section(&b, fmt.Sprintf("Capabilities (%d)", len(r.Capabilities)))
		for _, cap := range r.Capabilities {
			if cap.Ref != "" {
				fmt.Fprintf(&b, "  %s: %s\n", safeText(cap.Type), safeText(cap.Ref))
			} else {
				fmt.Fprintf(&b, "  %s\n", safeText(cap.Type))
			}
		}
	}
	renderExplainSurface(&b, r)
	if r.Readiness != nil {
		section(&b, "Readiness")
		passing := okStyle.Render("passing")
		if !r.Readiness.Passing {
			passing = errorStyle.Render("not passing")
		}
		styledField(&b, "status", passing)
		field(&b, "score", fmt.Sprintf("%d/%d", r.Readiness.Score, r.Readiness.MinScore))
		field(&b, "checks", fmt.Sprintf("%d done, %d partial, %d not done",
			r.Readiness.DoneCount, r.Readiness.PartialCount, r.Readiness.NotDoneCount))
	}
	return b.String()
}

// renderExplainSurface lists what the contract offers and needs: its
// interfaces, dependencies and environments, as `pacto explain` prints them.
func renderExplainSurface(b *strings.Builder, r *app.ExplainResult) {
	renderExplainInterfaces(b, r.Interfaces)
	renderExplainDependencies(b, r.Dependencies)
	if len(r.Environments) > 0 {
		section(b, fmt.Sprintf("Environments (%d)", len(r.Environments)))
		for _, env := range r.Environments {
			fmt.Fprintf(b, "  %s: %s\n", safeText(env.Name), safeText(env.File))
		}
	}
}

// renderExplainInterfaces lists each interface, with the guarantees it offers
// on a line of its own.
func renderExplainInterfaces(b *strings.Builder, ifaces []app.ExplainInterface) {
	if len(ifaces) == 0 {
		return
	}
	section(b, fmt.Sprintf("Interfaces (%d)", len(ifaces)))
	for _, iface := range ifaces {
		tail := lifecycleText(iface.Lifecycle)
		if iface.Visibility != "" {
			tail = ", " + iface.Visibility + tail
		}
		if iface.CompatibilityEpoch != 0 {
			tail += fmt.Sprintf(", compatibility epoch %d", iface.CompatibilityEpoch)
		}
		fmt.Fprintf(b, "  %s (%s%s)\n", safeText(iface.Name), safeText(iface.Type), safeText(tail))
		if g := iface.Guarantees; g != nil && g.Text(false) != "" {
			fmt.Fprintf(b, "    guarantees: %s\n", safeText(g.Text(false)))
		}
	}
}

// renderExplainDependencies lists each dependency and, as declared, what it
// requires of each provider interface.
func renderExplainDependencies(b *strings.Builder, deps []app.ExplainDependency) {
	if len(deps) == 0 {
		return
	}
	section(b, fmt.Sprintf("Dependencies (%d)", len(deps)))
	for _, dep := range deps {
		req := ""
		if dep.Required {
			req = " [required]"
		}
		ref := dep.Ref
		switch {
		case ref == "":
			ref = "external, version " + dep.Version + " (cannot be checked)"
		case dep.Compatibility != "":
			ref += " (" + dep.Compatibility + ")"
		}
		fmt.Fprintf(b, "  %s: %s%s\n", safeText(dep.Name), safeText(ref), req)
		for _, r := range dep.Requires {
			fmt.Fprintf(b, "    requires from %s\n", safeText(r.String()))
		}
	}
}

// renderFleetExplain reports the structured reasons for a subject's state.
func renderFleetExplain(r *fleet.ExplainResult) string {
	if r == nil {
		return "(no result)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s\n", safeText(r.Kind), safeText(r.Subject), safeText(r.Status))
	if len(r.Reasons) == 0 {
		b.WriteString(dimStyle.Render("  no reasons recorded\n"))
		return b.String()
	}
	section(&b, "Reasons")
	for _, reason := range r.Reasons {
		fmt.Fprintf(&b, "  [%s] %s\n", safeText(reason.Code), safeText(reason.Message))
	}
	return b.String()
}

// renderLock reports the lock check outcome. UpToDate means the check passed,
// Written means a lock was created or updated, neither should not happen.
func renderLock(r *app.LockResult) string {
	if r == nil {
		return "(no result)"
	}
	var b strings.Builder
	if r.UpToDate {
		b.WriteString(okStyle.Render("up to date") + "\n")
	} else if r.Written {
		b.WriteString(okStyle.Render("written") + "  " + safeText(r.Path) + "\n")
	}
	field(&b, "dependencies", fmt.Sprintf("%d", r.Dependencies))
	field(&b, "references", fmt.Sprintf("%d", r.References))
	return b.String()
}

// renderGate is the release gate line, empty when the answer carries none.
func renderGate(r *impact.Result) string {
	switch r.Gate {
	case impact.GateBlocked:
		return errorStyle.Render("blocked: `pacto impact` exits 1") + "  " + safeText(r.GateReason) + "\n"
	case impact.GateUnverified:
		return warnStyle.Render("unverified: `pacto impact` exits 2") + "  " + safeText(r.GateReason) + "\n"
	case impact.GateClear:
		return okStyle.Render("clear") + "  " + safeText(r.GateReason) + "\n"
	}
	return ""
}

// renderImpact reports the blast radius of a change and the release gate
// `pacto impact` exits on: blocked (1), unverified (2) or clear (0).
func renderImpact(r *impact.Result) string {
	if r == nil {
		return "(no result)"
	}
	var b strings.Builder
	b.WriteString(renderGate(r))
	if r.Classification == "BREAKING" {
		b.WriteString(errorStyle.Render("BREAKING") + "  " + safeText(r.Service) + " " + safeText(r.OldVersion) + " -> " + safeText(r.NewVersion) + "\n")
	} else {
		b.WriteString(safeText(r.Classification) + "  " + safeText(r.Service) + " " + safeText(r.OldVersion) + " -> " + safeText(r.NewVersion) + "\n")
	}
	if len(r.BreakingChanges) > 0 {
		section(&b, fmt.Sprintf("Breaking changes (%d)", len(r.BreakingChanges)))
		for _, c := range r.BreakingChanges {
			fmt.Fprintf(&b, "  [%s] %s (%s): %s\n", c.Classification, safeText(c.Path), c.Type, safeText(c.Reason))
		}
	}
	if len(r.PotentiallyBreakingChanges) > 0 {
		section(&b, fmt.Sprintf("Potentially breaking changes (%d)", len(r.PotentiallyBreakingChanges)))
		for _, c := range r.PotentiallyBreakingChanges {
			fmt.Fprintf(&b, "  [%s] %s (%s): %s\n", c.Classification, safeText(c.Path), c.Type, safeText(c.Reason))
		}
	}
	if len(r.Consumers) == 0 {
		section(&b, "Affected consumers")
		b.WriteString(dimStyle.Render("  none identified\n"))
	} else {
		section(&b, fmt.Sprintf("Affected consumers (%d)", len(r.Consumers)))
		for _, cons := range r.Consumers {
			renderConsumer(&b, cons)
		}
	}
	if len(r.Owners) > 0 {
		section(&b, fmt.Sprintf("Owners to review (%d)", len(r.Owners)))
		for _, owner := range r.Owners {
			fmt.Fprintf(&b, "  %s\n", safeText(owner))
		}
	}
	return b.String()
}

// renderConsumer writes one affected consumer: its verdict line, every
// guarantee it refuses and its note.
func renderConsumer(b *strings.Builder, cons impact.AffectedConsumer) {
	depth := ""
	if cons.Direct {
		depth = " [direct]"
	} else {
		depth = fmt.Sprintf(" [depth %d]", cons.Depth)
	}
	compat := safeText(cons.CompatibilityVerdict)
	if compat == impact.CompatibilityIncompatible {
		compat = errorStyle.Render(compat)
	}
	targets := fmt.Sprintf("%d targets", len(cons.Targets))
	if len(cons.Targets) == 0 {
		targets = dimStyle.Render("no targets")
	}
	fmt.Fprintf(b, "  %s%s: %s, %s, %s\n", safeText(cons.Service), depth, compat, safeText(string(cons.Confidence)), targets)
	for _, m := range cons.Unmet {
		b.WriteString("    " + errorStyle.Render("refused: "+safeText(m.String())) + "\n")
	}
	if cons.Note != "" {
		b.WriteString("    " + dimStyle.Render(safeText(cons.Note)) + "\n")
	}
}
