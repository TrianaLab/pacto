package cli

import (
	"fmt"
	"io"
	"math"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// printStats prints a stats answer as JSON or text. Text says every
// limitation on stderr, so a partial answer never reads as complete.
func printStats(cmd *cobra.Command, st *stats.Stats, format string) error {
	return formatResult(cmd, format, st, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(w, statsHeader(st))
		if st.Totals == nil {
			// No records is a sentence, never a table of zeros.
			for _, l := range st.Limitations {
				if l.Code == stats.LimitationNoRecords {
					_, _ = fmt.Fprintln(w, l.Message)
				}
			}
		} else {
			printStatsGroups(w, st)
			printStatsEnvironments(w, st.Totals.Environments)
		}
		warnPartial(cmd, fleet.Meta{AsOf: st.AsOf, Completeness: st.Completeness, Limitations: st.Limitations})
		return nil
	}, nil)
}

func statsHeader(st *stats.Stats) string {
	h := fmt.Sprintf("asOf %s  window %s to %s  records %d read, %d skipped (%s)  %s",
		st.AsOf.Format(time.RFC3339), st.Window.Since.Format(time.RFC3339), st.Window.Until.Format(time.RFC3339),
		st.RecordsRead, st.Skipped.Total(), st.Completeness, st.Provenance)
	if a := st.Attribution; a != nil {
		h += fmt.Sprintf("; %s %s as of %s", st.By, a.Provenance, a.AsOf.Format(time.RFC3339))
	}
	return h
}

// printStatsGroups prints one row per group, or one row of totals when the
// answer has no groups.
func printStatsGroups(w io.Writer, st *stats.Stats) {
	by, rows := "TOTAL", []stats.Group{{Label: "all", Metrics: *st.Totals}}
	if st.By != "" {
		by, rows = strings.ToUpper(st.By), st.Groups
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "%s\tBREAKING CAUGHT\tCONSUMERS PROTECTED\tCLEAR\tBLOCKED\tUNVERIFIED\n", by)
	for _, g := range rows {
		m := g.Metrics
		_, _ = fmt.Fprintf(tw, "%s\t%d (%d runs)\t%d (%d unknown)\t%d\t%d\t%d\n", g.Label,
			m.BreakingCaught.Distinct, m.BreakingCaught.Runs, m.ConsumersProtected.Count, m.ConsumersProtected.Unknown,
			m.Gates.Clear, m.Gates.Blocked, m.Gates.Unverified)
	}
	_ = tw.Flush()
}

// printStatsEnvironments prints the delivery numbers per environment. The
// order is alphabetical and says so: names imply no promotion order.
func printStatsEnvironments(w io.Writer, envs []stats.EnvironmentMetrics) {
	if len(envs) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nEnvironments (alphabetical)")
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ENVIRONMENT\tOPERATIONS\tHELD\tFAILED\tROLLED BACK\tVERIFICATION FAILED\tLEAD TIME p50\tp90")
	for _, e := range envs {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n", e.Environment, e.Operations,
			ratioText(e.Held), ratioText(e.Failed), ratioText(e.RolledBack), ratioText(e.VerificationFailed),
			leadText(e.LeadTime, e.LeadTime.P50Seconds), leadText(e.LeadTime, e.LeadTime.P90Seconds))
	}
	_ = tw.Flush()
}

func ratioText(r stats.Ratio) string {
	if r.Unknown || r.Of == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d/%d (%d%%)", r.Count, r.Of, int(math.Round(float64(r.Count)*100/float64(r.Of))))
}

func leadText(lt stats.LeadTime, seconds *int64) string {
	if lt.Unknown || seconds == nil {
		return fmt.Sprintf("unknown (%d unjoined)", lt.Unjoined)
	}
	return (time.Duration(*seconds) * time.Second).String()
}
