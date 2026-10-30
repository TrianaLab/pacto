package doc

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/contractview"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// now is the clock a sunset is read against; tests replace it.
var now = time.Now

// The behavior half of a contract (pactoVersion 2.2 and 2.3) in the words the
// pacto doc HTML page uses: an interface's retirement schedule, the guarantees
// it offers and its compatibility epoch, what a dependency requires and whether
// the provider meets it, how each validation stage ended and each
// environment's check. Every verdict was computed by contractview; these
// writers only name it.

// guaranteeTerms renders guarantees as "Delivery `at-least-once` · Idempotent
// `yes`"; required spells out each duration's bound ("Timeout at most `5s`").
func guaranteeTerms(g contract.Guarantees, required bool) string {
	var parts []string
	for _, t := range g.Terms() {
		bound := ""
		if required && t.Bound != "" {
			bound = t.Bound + " "
		}
		parts = append(parts, capitalizeFirst(t.Name)+" "+bound+dash(yes(t.Value)))
	}
	return strings.Join(parts, " · ")
}

func yes(v string) string {
	if v == "true" {
		return "yes"
	}
	return v
}

// lifecycleLine is a deprecated interface's state and schedule, louder once
// its sunset has passed, when the interface may be removed in any release.
func lifecycleLine(l *contract.InterfaceLifecycle) string {
	state, tail := "Deprecated", ""
	if l.SunsetReached(now()) {
		state, tail = "Past sunset", ". It may be removed in any release."
	}
	s := fmt.Sprintf("**%s**: deprecated %s", state, dash(l.Deprecated))
	if l.Sunset != "" {
		s += " · sunset " + dash(l.Sunset)
	}
	if l.Successor != "" {
		s += " · successor " + dash(l.Successor)
	}
	return s + tail
}

// writeInterfaceBehavior lists, under the interface table, each interface's
// lifecycle, guarantees and compatibility epoch. Interfaces with none of them
// get no entry.
func writeInterfaceBehavior(b *strings.Builder, ifaces []contractview.InterfaceInfo) {
	wrote := false
	for _, i := range ifaces {
		var terms string
		if i.Guarantees != nil {
			terms = guaranteeTerms(*i.Guarantees, false)
		}
		if i.Lifecycle == nil && terms == "" && i.CompatibilityEpoch == 0 {
			continue
		}
		wrote = true
		fmt.Fprintf(b, "- **%s**\n", dash(i.Name))
		if i.Lifecycle != nil {
			fmt.Fprintf(b, "  - %s\n", lifecycleLine(i.Lifecycle))
		}
		if terms != "" {
			fmt.Fprintf(b, "  - Guarantees: %s\n", terms)
		}
		if i.CompatibilityEpoch != 0 {
			fmt.Fprintf(b, "  - Compatibility epoch `%d`\n", i.CompatibilityEpoch)
		}
	}
	if wrote {
		fmt.Fprintln(b)
	}
}

// guaranteeStatus names a dependency's guarantee check. Refused and Not
// verified stay apart: a check that could not run is never a refusal, and met
// comes only from a provider contract that was checked.
func guaranteeStatus(dep contractview.DependencyInfo) string {
	switch dep.GuaranteeStatus {
	case fleet.GuaranteeMet:
		return ": Guarantees met"
	case fleet.GuaranteeUnmet:
		return ": Refused"
	case fleet.GuaranteeUnknown:
		if dep.GuaranteeReason != "" {
			return ": Not verified: " + dashText(dep.GuaranteeReason) + "."
		}
		return ": Not verified"
	}
	return ""
}

// mismatchBound is how an offered duration has to compare with a required one,
// keyed by [contract.Mismatch] guarantee name.
var mismatchBound = map[string]string{"dedupeWindow": "at least ", "retention": "at least ", "timeout": "at most "}

// mismatchText names one guarantee a provider refuses.
func mismatchText(m contract.Mismatch) string {
	if m.Guarantee == "interface" {
		return dash(m.Interface) + ": the provider declares no such interface"
	}
	name := capitalizeFirst(strings.ReplaceAll(m.Guarantee, "dedupeWindow", "dedupe window"))
	offered := "nothing offered"
	if m.Offered != "" {
		offered = dash(yes(m.Offered)) + " offered"
	}
	return fmt.Sprintf("%s: %s %s%s required, %s", dash(m.Interface), name, mismatchBound[m.Guarantee], dash(yes(m.Required)), offered)
}

// writeDependencyBehavior lists, under the dependency table, what each
// dependency requires of its provider, whether the provider meets it and the
// provider's deprecated interfaces. Dependencies with none of them get no entry.
func writeDependencyBehavior(b *strings.Builder, deps []contractview.DependencyInfo) {
	wrote := false
	for _, dep := range deps {
		if len(dep.Requires) == 0 && len(dep.Unmet) == 0 && len(dep.DeprecatedInterfaces) == 0 {
			continue
		}
		wrote = true
		fmt.Fprintf(b, "- **%s**%s\n", dash(dep.Name), guaranteeStatus(dep))
		for _, r := range dep.Requires {
			fmt.Fprintf(b, "  - Requires from %s", dash(r.Interface))
			if terms := guaranteeTerms(r.Guarantees, true); terms != "" {
				fmt.Fprintf(b, ": %s", terms)
			}
			fmt.Fprintln(b)
		}
		for _, m := range dep.Unmet {
			fmt.Fprintf(b, "  - Refused: %s\n", mismatchText(m))
		}
		for _, d := range dep.DeprecatedInterfaces {
			l := contract.InterfaceLifecycle{Deprecated: d.Deprecated, Sunset: d.Sunset, Successor: d.Successor}
			fmt.Fprintf(b, "  - Interface %s · %s\n", dash(d.Interface), lifecycleLine(&l))
		}
	}
	if wrote {
		fmt.Fprintln(b)
	}
}

// ── Environments ──────────────────────────────────────────────────────

var environmentLabel = map[string]string{
	fleet.EnvironmentPass: "Passed", fleet.EnvironmentFail: "Failed",
	fleet.EnvironmentUnverified: "Not verified", fleet.EnvironmentSkip: "Not checked",
}

// environmentSummary says what needs attention: a failure, or a value set
// Pacto could not check. Neither of the last two is a pass.
func environmentSummary(envs []fleet.EnvironmentResult) string {
	var parts []string
	for _, st := range []string{fleet.EnvironmentFail, fleet.EnvironmentUnverified, fleet.EnvironmentSkip} {
		n := 0
		for _, e := range envs {
			if e.Status == st {
				n++
			}
		}
		if n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+strings.ToLower(environmentLabel[st]))
		}
	}
	switch len(parts) {
	case 0:
		return "Every environment passed."
	case 1:
		return parts[0] + "."
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1] + "."
}

// writeEnvironments lists each environment, the check of its values file and
// its findings, located in that file.
func writeEnvironments(b *strings.Builder, d *contractview.ServiceDetails, num *sectionNumberer) {
	if len(d.Environments) == 0 {
		return
	}
	fmt.Fprintf(b, "## %s. Environments\n\n%s\n\n", num.Next(1), environmentSummary(d.Environments))
	fmt.Fprintln(b, "| Environment | File | Check |")
	fmt.Fprintln(b, "|-------------|------|-------|")
	for _, e := range d.Environments {
		check := environmentLabel[e.Status]
		if check == "" {
			check = "Unknown"
		}
		if e.Status == fleet.EnvironmentSkip && e.Reason != "" {
			check += ": " + dashText(e.Reason)
		}
		fmt.Fprintf(b, "| %s | %s | %s |\n", dash(e.Name), dash(e.File), check)
	}
	fmt.Fprintln(b)
	writeEnvironmentFindings(b, d.Environments)
}

func writeEnvironmentFindings(b *strings.Builder, envs []fleet.EnvironmentResult) {
	var notes []string
	header := false
	for _, e := range envs {
		for _, f := range e.Findings.Items {
			if !header {
				fmt.Fprintln(b, "| Environment | Severity | Code | Message | Location |")
				fmt.Fprintln(b, "|-------------|----------|------|---------|----------|")
				header = true
			}
			// Every message names its environment, which the row already does.
			msg := strings.TrimPrefix(f.Message, "environment "+e.Name+": ")
			fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n", dash(e.Name), f.Severity, dash(string(f.Code)), dashText(msg), dash(locationText(f)))
		}
		if e.Findings.Truncated {
			notes = append(notes, fmt.Sprintf("_%s: showing %d of %d findings._", dash(e.Name), e.Findings.Count, e.Findings.Total))
		}
	}
	if header {
		fmt.Fprintln(b)
	}
	for _, n := range notes {
		fmt.Fprintf(b, "%s\n\n", n)
	}
}

// locationText is where a finding points in its file: "envs/prod.yaml:3:5".
func locationText(f fleet.LocatedFinding) string {
	if f.File == "" {
		return ""
	}
	s := f.File
	if f.Line > 0 {
		s += ":" + strconv.Itoa(f.Line)
		if f.Column > 0 {
			s += ":" + strconv.Itoa(f.Column)
		}
	}
	return s
}

// ── Validation ────────────────────────────────────────────────────────

var stageName = map[string]string{"structural": "Structure", "cross-field": "Cross-field", "policy": "Policy", "environments": "Environments"}
var stageResult = map[string]string{"pass": "passed", "fail": "failed", "skip": "not run"}

// writeValidation shows how each validation stage ended, so a stage that did
// not run is never read as a pass, and the errors and warnings it found.
func writeValidation(b *strings.Builder, d *contractview.ServiceDetails, num *sectionNumberer) {
	if !hasValidation(d) {
		return
	}
	fmt.Fprintf(b, "## %s. Validation\n\n", num.Next(1))
	if len(d.Stages) > 0 {
		fmt.Fprintln(b, "| Stage | Result | Reason |")
		fmt.Fprintln(b, "|-------|--------|--------|")
		for _, st := range d.Stages {
			fmt.Fprintf(b, "| %s | %s | %s |\n", orSelf(stageName, st.Name), orSelf(stageResult, st.Status), dashText(st.Reason))
		}
		fmt.Fprintln(b)
	}
	if v := d.Validation; v != nil {
		writeIssues(b, "Errors", v.Errors)
		writeIssues(b, "Warnings", v.Warnings)
	}
}

func orSelf(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return k
}

func writeIssues(b *strings.Builder, title string, issues []contractview.ValidationIssue) {
	if len(issues) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s**\n\n", title)
	fmt.Fprintln(b, "| Code | Path | Message |")
	fmt.Fprintln(b, "|------|------|---------|")
	for _, is := range issues {
		fmt.Fprintf(b, "| %s | %s | %s |\n", dash(is.Code), dash(is.Path), dashText(is.Message))
	}
	fmt.Fprintln(b)
}
