package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func newFleetMapCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "map",
		Short: "Show the whole fleet as groups, one group's services or one service's callers and callees",
		Long: "Shows the whole fleet as groups by business domain, owner team or environment, each with how many services " +
			"it holds, how much of what Pacto can observe matches what the contracts declare, how complete its contracts are " +
			"and what needs attention, above the fleet's declared and observed match and contract completeness. With --group " +
			"it lists one group's services page by page; with --focus it lists one service's callers and callees, one or two " +
			"hops out, nine at most a column, worst first, and the calls between them. Environments read by name, and the " +
			"answer says so: the dashboard orders them by the Stages the signed-in person may read.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: noCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := mapRequestFromFlags(cmd)
			if err != nil {
				return err
			}
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			return runFleetMap(cmd, q, r, v.GetString(outputFormatKey))
		},
	}
	f := cmd.Flags()
	f.String("group-by", "", "grouping: domain (default), team or environment")
	f.String("environment", "", "environment the services are read in: with --group, each says whether it runs there and its version")
	f.String("group", "", "list this group's services, by the key the map names")
	f.Int("offset", 0, "with --group, the first service of the page")
	f.Int("limit", 0, fmt.Sprintf("with --group, services a page (0 = %d, at most %d)", fleet.DefaultMapNodes, fleet.MaxMapNodes))
	f.String("focus", "", "list this service's callers and callees: a name, or its key when the name is used in more than one domain")
	f.Int("hops", 0, "with --focus, hops out on each side (0 = 1, at most 2)")
	_ = cmd.RegisterFlagCompletionFunc("group-by", staticCompletions(fleet.MapGroupByDomain, fleet.MapGroupByTeam, fleet.MapGroupByEnvironment))
	return cmd
}

// mapRequest is one fleet map question: the groups, one group's page or one
// service's focus.
type mapRequest struct {
	groupBy, environment, group, focus string
	hops, offset, limit                int
}

func mapRequestFromFlags(cmd *cobra.Command) (mapRequest, error) {
	f := cmd.Flags()
	var r mapRequest
	r.groupBy, _ = f.GetString("group-by")
	r.environment, _ = f.GetString("environment")
	r.group, _ = f.GetString("group")
	r.focus, _ = f.GetString("focus")
	r.hops, _ = f.GetInt("hops")
	r.offset, _ = f.GetInt("offset")
	r.limit, _ = f.GetInt("limit")
	return r, r.check()
}

// check refuses a flag the question it asks does not read.
func (r mapRequest) check() error {
	switch {
	case r.focus != "" && (r.group != "" || r.groupBy != "" || r.environment != ""):
		return errors.New("--focus reads every environment and no grouping: drop --group, --group-by and --environment")
	case r.focus == "" && r.hops != 0:
		return errors.New("--hops needs --focus")
	case r.group == "" && (r.offset != 0 || r.limit != 0):
		return errors.New("--offset and --limit need --group")
	}
	return nil
}

func runFleetMap(cmd *cobra.Command, q *fleet.Query, r mapRequest, format string) error {
	switch {
	case r.focus != "":
		res, err := q.MapFocus(fleet.MapFocusQuery{Service: r.focus, Hops: r.hops})
		if err != nil {
			return err
		}
		return formatResult(cmd, format, res, func() error { printMapFocus(cmd, res); return nil }, nil)
	case r.group != "":
		res, err := q.MapNodes(fleet.MapNodesQuery{GroupBy: r.groupBy, Group: r.group, Environment: r.environment, Offset: r.offset, Limit: r.limit})
		if err != nil {
			return err
		}
		return formatResult(cmd, format, res, func() error { printMapNodes(cmd, res); return nil }, nil)
	}
	res, err := q.Map(fleet.MapQuery{GroupBy: r.groupBy, Environment: r.environment})
	if err != nil {
		return err
	}
	return formatResult(cmd, format, res, func() error { printFleetMap(cmd, res); return nil }, nil)
}

// pctText is a percentage, or unknown when there is none.
func pctText(p *int) string {
	if p == nil {
		return "unknown"
	}
	return strconv.Itoa(*p) + "%"
}

func printFleetMap(cmd *cobra.Command, m *fleet.FleetMap) {
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(w, "asOf %s  completeness %s\n", m.Meta.AsOf.Format(time.RFC3339), m.Meta.Completeness)
	printMapMatch(w, m.Match)
	printContractCompleteness(w, m.Knowledge.ContractCompleteness)
	how := "Grouped by " + m.GroupBy
	if m.Nested {
		how += ", each team inside its domain"
	}
	if m.EnvironmentOrder == "name" {
		how += "; environments by name"
	}
	_, _ = fmt.Fprintf(w, "%s: %d groups · services %d (%d inferred) · external systems %d\n", how, len(m.Groups), m.Totals.Services, m.Totals.Inferred, m.Totals.Externals)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "GROUP\tKIND\tSERVICES\tINFERRED\tEXTERNAL\tMATCH\tCOMPLETE\tERRORS\tWARNINGS\tWORST")
	for _, g := range m.Groups {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%s\t%d\t%d\t%s\n", g.Key, g.Kind, g.Services, g.Inferred, g.Externals,
			orDash(optPct(g.Match.Percent)), orDash(optPct(g.PercentComplete)), g.Attention.Errors, g.Attention.Warnings, orDash(worstText(g.Attention.Worst)))
	}
	_ = tw.Flush()
	if m.RibbonsTruncated {
		_, _ = fmt.Fprintf(w, "truncated: the %d largest of %d links between groups are kept\n", len(m.Ribbons), m.Totals.Ribbons)
	}
	warnProductPartial(cmd, m.Meta)
}

// printMapMatch prints the declared and observed match, with its base.
func printMapMatch(w io.Writer, t fleet.MatchTally) {
	line := "Declared ↔ observed match: " + pctText(t.Percent) + " of what Pacto can observe"
	if t.Percent == nil {
		line = "Declared ↔ observed match: unknown · Pacto observes nothing yet"
	}
	_, _ = fmt.Fprintf(w, "%s · %s observed coverage · unknown never counts as matched\n", line, pctText(t.Coverage))
	for _, c := range []struct {
		name string
		n    fleet.MatchCounts
	}{{"services", t.Services}, {"dependencies", t.Dependencies}} {
		_, _ = fmt.Fprintf(w, "  %s: %d matched · %d expected only · %d observed only · %d insufficient\n", c.name, c.n.Matched, c.n.ExpectedOnly, c.n.ObservedOnly, c.n.Insufficient)
	}
}

// optPct is a percentage, or "" when there is none.
func optPct(p *int) string {
	if p == nil {
		return ""
	}
	return pctText(p)
}

func worstText(cs []fleet.AttentionCategoryCount) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = fmt.Sprintf("%s %d", c.Category, c.Count)
	}
	return strings.Join(out, ", ")
}

func printMapNodes(cmd *cobra.Command, p *fleet.MapNodes) {
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(w, "Group %s (%s): %d services from offset %d of %d\n", p.Group, p.GroupBy, len(p.Nodes), p.Offset, p.Total)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SERVICE\tMATCH\tSEVERITY\tWORST\tVERSION\tOWNER\tIN\tOUT\tCOMPLETE\tMISSING")
	for _, n := range p.Nodes {
		name := n.Name
		switch {
		case n.Inferred:
			name += " (inferred)"
		case n.InEnvironment != nil && !*n.InEnvironment:
			name += " (not here)"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n", name, orDash(n.Match), orDash(n.Severity), orDash(n.Worst), orDash(n.Version), orDash(n.Owner),
			n.In, n.Out, orDash(optPct(n.PercentComplete)), orDash(strings.Join(n.Missing, ", ")))
	}
	_ = tw.Flush()
	if p.Truncated {
		_, _ = fmt.Fprintf(w, "truncated: more services follow; ask --offset %d\n", p.Offset+len(p.Nodes))
	}
	warnProductPartial(cmd, p.Meta)
}

func printMapFocus(cmd *cobra.Command, f *fleet.MapFocusAnswer) {
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(w, "Focus %s (group %s): match %s · %s\n", f.Focus.Name, f.Group, orDash(f.Focus.Match), orDash(f.Focus.Severity))
	for _, c := range f.Columns {
		side := "Callers"
		if c.Hop > 0 {
			side = "Callees"
		}
		_, _ = fmt.Fprintf(w, "%s at %d hop: %d of %d\n", side, max(c.Hop, -c.Hop), len(c.Nodes), c.Total)
		for _, n := range c.Nodes {
			_, _ = fmt.Fprintf(w, "  %s  match=%s  %s\n", n.Name, orDash(n.Match), orDash(n.Severity))
		}
	}
	if len(f.Edges) > 0 {
		_, _ = fmt.Fprintln(w, "Calls:")
	}
	for _, e := range f.Edges {
		labels := strings.TrimSpace(e.Interface + " " + e.Range)
		problem := ""
		if e.Problem != nil {
			problem = fmt.Sprintf("  %s %s: %s", e.Problem.Severity, e.Problem.Code, e.Problem.Reason)
		}
		_, _ = fmt.Fprintf(w, "  %s -> %s  %s  %s  %s%s\n", e.From.Label, e.To.Label, orDash(labels), e.Provenance, orDash(e.Difference), problem)
	}
	warnProductPartial(cmd, f.Meta)
}
