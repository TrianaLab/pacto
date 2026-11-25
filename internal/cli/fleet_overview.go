package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/shellquote"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func newFleetOverviewCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "overview",
		Short: "Summarize the organization's operational health and knowledge",
		Long: "Summarizes the organization in one answer: target compliance (with how many compliant targets rest on " +
			"stale evidence), operational health, the knowledge gaps the contracts leave, ownership, evaluation " +
			"coverage, the business domains and where to look next. A target is failing when its own health check " +
			"reports it is not healthy and unknown when Pacto cannot tell; unknown is never a pass.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			return printFleetOverview(cmd, q.Overview(), v.GetString(outputFormatKey))
		},
	}
}

func printFleetOverview(cmd *cobra.Command, ov *fleet.Overview, format string) error {
	fleet.KeepServedTools(ov.Attention.Items, fleet.NoTools) // the CLI serves no MCP tool
	return formatResult(cmd, format, ov, func() error {
		w := cmd.OutOrStdout()
		s := &ov.Summary
		printOverviewHeader(w, ov)
		printOverviewTargets(w, s)
		_, _ = fmt.Fprintf(w, "Evidence: %d targets with evidence (%d stale) · %d never observed\n", s.Evidence.WithEvidence, s.Evidence.Stale, s.Evidence.WithoutEvidence)
		h := s.OperationalHealth
		_, _ = fmt.Fprintf(w, "Health: healthy %d · failing %d · unknown %d · not observed %d%s\n",
			h.Available, h.Unavailable, h.Unknown, h.NotObserved, countSuffix(h.Degraded, "degraded"))
		printOverviewKnowledge(w, s.Knowledge)
		o := s.Ownership
		_, _ = fmt.Fprintf(w, "Owners: %d declared · %d disputed · %d none declared · %d unknown\n", o.Consistent, o.Conflicting, o.Unowned, o.Unknown)
		c := s.Coverage
		_, _ = fmt.Fprintf(w, "Coverage: %d of %d required assertions evaluated on %d targets; %d report none\n", c.Evaluated, c.Required, c.Reporting, c.NotReporting)
		printOverviewDomains(w, s.ByDomain)
		printIncompleteSources(w, ov.Meta.Sources, ov.Meta.SourcesTruncated)
		src := sourceArgsText(cmd)
		for _, ep := range ov.EntryPoints {
			if next := entryPointCommand(ep); next != "" {
				_, _ = fmt.Fprintf(w, "Next: %s%s  (%s: %d)\n", next, src, ep.Label, ep.Count)
			}
		}
		warnProductPartial(cmd, ov.Meta)
		return nil
	}, nil)
}

func printOverviewHeader(w io.Writer, ov *fleet.Overview) {
	incomplete := ""
	if n := ov.Summary.DegradedSources + ov.Summary.StaleSources + ov.Summary.UnavailableSources; n > 0 {
		incomplete = fmt.Sprintf(" (%d sources incomplete)", n)
	}
	_, _ = fmt.Fprintf(w, "asOf %s  completeness %s%s\n", ov.Meta.AsOf.Format(time.RFC3339), ov.Meta.Completeness, incomplete)
}

// printOverviewTargets prints the target compliance partition: the three
// buckets every fleet has, then each other bucket that holds a target.
func printOverviewTargets(w io.Writer, s *fleet.OverviewSummary) {
	line := fmt.Sprintf("Targets %d: compliant %d (%d stale) · not compliant %d · unknown %d",
		s.Targets, s.CompliantTargets, s.StaleCompliantTargets, s.NonCompliantTargets, s.UnknownTargets)
	for _, b := range []struct {
		n    int
		name string
	}{
		{s.WarningTargets, "warning"}, {s.InvalidTargets, "invalid"}, {s.ReferenceTargets, "reference"},
		{s.NotEvaluatedTargets, "not evaluated"}, {s.OtherComplianceTargets, "other"},
	} {
		line += countSuffix(b.n, b.name)
	}
	_, _ = fmt.Fprintln(w, line)
}

// countSuffix is " · <name> <n>" when n is not zero.
func countSuffix(n int, name string) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" · %s %d", name, n)
}

func printOverviewKnowledge(w io.Writer, k fleet.KnowledgeTally) {
	g := k.Gaps
	_, _ = fmt.Fprintf(w, "Knowledge: %d services · %d fully described · %d with gaps · %d no summary · %d no domain · "+
		"%d no interface document · %d no readiness assessment · %d run nowhere · %d unverified capability · %d unknown\n",
		k.Services, k.Described, k.WithGaps, g.NoSummary, g.NoDomain, g.NoInterfaceDocs, g.NoReadiness, g.NotDeployed, g.UnverifiedCapability, k.Unknown)
	printContractCompleteness(w, k.ContractCompleteness)
}

// printContractCompleteness prints the fleet's contract completeness line,
// with its judged base, when it counts any service.
func printContractCompleteness(w io.Writer, c fleet.FleetCompleteness) {
	if c.Judged+c.Unknown == 0 {
		return
	}
	pct := "unknown"
	if c.PercentComplete != nil {
		pct = fmt.Sprintf("%d%%", *c.PercentComplete)
	}
	line := fmt.Sprintf("Contract completeness: %s · %d of %d judged services fully described", pct, c.FullyDescribed, c.Judged)
	if c.Unknown > 0 {
		line += fmt.Sprintf(" · %d not judged", c.Unknown)
	}
	missing := make([]string, 0, len(c.MostMissing))
	for _, m := range c.MostMissing {
		missing = append(missing, fmt.Sprintf("%s %d", m.Section, m.Count))
	}
	if len(missing) > 0 {
		line += " · most missing: " + strings.Join(missing, ", ")
	}
	_, _ = fmt.Fprintln(w, line)
}

func printOverviewDomains(w io.Writer, rows []fleet.DomainTally) {
	if len(rows) == 0 {
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	// Compliance, staleness and health count targets; the rest count services.
	_, _ = fmt.Fprintln(tw, "DOMAIN\tTARGETS\tNC-TARGETS\tUNKNOWN-TARGETS\tSTALE-TARGETS\tFAILING-TARGETS\tSERVICES\tGAPS\tNO-OWNER\tOWNER-UNKNOWN\tRUN-NOWHERE")
	for _, d := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n", domainLabel(d), d.Targets, d.NonCompliant, d.Unknown, d.Stale, d.FailingHealth,
			d.Services, d.WithGaps, d.Unowned, d.OwnerUnknown, d.NotDeployed)
	}
	_ = tw.Flush()
}

// domainLabel names a domain row the way the --business-domain filter takes it.
func domainLabel(d fleet.DomainTally) string {
	switch {
	case d.DomainUnknown:
		return fleet.UnknownBusinessDomain
	case d.Domain == "":
		return fleet.NoBusinessDomain
	}
	return d.Domain
}

// printIncompleteSources lists the sources that are not available from the
// answer's bounded meta, and says so when that list was cut. An advisory
// source never makes the answer incomplete, so it is named apart.
func printIncompleteSources(w io.Writer, sources []fleet.SourceState, truncated bool) {
	for _, s := range sources {
		if s.Status == fleet.SourceAvailable || s.Advisory {
			continue
		}
		reason := ""
		if s.Error != nil {
			reason = ": " + s.Error.Message
		}
		_, _ = fmt.Fprintf(w, "Incomplete source: %s (%s) %s%s\n", s.ID, s.Kind, s.Status, reason)
	}
	if truncated {
		_, _ = fmt.Fprintln(w, "... and more incomplete sources; the sources Next line lists them all")
	}
	printAdvisorySources(w, sources)
}

// sourceArgsText is the source flags the overview ran with, shell-quoted with
// a leading space, so a printed Next line reads the same snapshot.
func sourceArgsText(cmd *cobra.Command) string {
	out := ""
	for _, a := range fleetSourceArgs(cmd) {
		out += " " + shellquote.Quote(a)
	}
	return out
}

// entryPointCommand is the CLI command that lists what an attention or
// sources entry point counts, or "" for any other view.
func entryPointCommand(ep fleet.EntryPoint) string {
	switch ep.View {
	case fleet.EntryPointSources:
		return "pacto fleet entities --kind source --source-health " + fleet.SourceHealthIncomplete
	case fleet.EntryPointAttention:
	default:
		return ""
	}
	if ep.Category == "" {
		return "pacto fleet attention"
	}
	return "pacto fleet attention --category " + ep.Category
}
