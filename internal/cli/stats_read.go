package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// statsNow is the clock of one stats answer: read once per command and used
// for both the window and the aggregation. A var so tests can fix it.
var statsNow = time.Now

// statsKinds is the --kind vocabulary: the run record kinds.
var statsKinds = []string{runrecord.KindDiff, runrecord.KindImpact, runrecord.KindPublish, runrecord.KindOperation}

// statsQuery checks and builds what a stats answer totals. Each kinds value
// may hold several comma-separated kinds, so a repeated flag and one MCP
// string share it.
func statsQuery(since, until, by, service string, kinds []string, now time.Time) (stats.Query, error) {
	if by != "" && !slices.Contains(stats.GroupBy, by) {
		return stats.Query{}, fmt.Errorf("unsupported by %q: give one of %s", by, strings.Join(stats.GroupBy, ", "))
	}
	var ks []string
	for _, v := range kinds {
		for k := range strings.SplitSeq(v, ",") {
			if k = strings.TrimSpace(k); k == "" {
				continue
			}
			if !slices.Contains(statsKinds, k) {
				return stats.Query{}, fmt.Errorf("unsupported kind %q: give one of %s", k, strings.Join(statsKinds, ", "))
			}
			ks = append(ks, k)
		}
	}
	w, err := stats.ParseWindow(since, until, now)
	if err != nil {
		return stats.Query{}, err
	}
	return stats.Query{Window: w, By: by, Service: service, Kinds: ks}, nil
}

// statsAttribution names the fleet snapshot owner and domain come from.
func statsAttribution(q *fleet.Query) *stats.Attribution {
	m := q.ProductMeta()
	return &stats.Attribution{AsOf: m.AsOf, SnapshotID: m.SnapshotID, Partial: m.Completeness != fleet.CompletenessComplete}
}

// runStats reads, totals and prints the run records of the stats repository.
func runStats(svc *app.Service, v *viper.Viper) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		now := statsNow()
		repo := flagOrEnv(cmd, "registry", "PACTO_STATS_REGISTRY")
		if repo == "" {
			return errNoStatsRegistry
		}
		f := cmd.Flags()
		since, _ := f.GetString("since")
		until, _ := f.GetString("until")
		by, _ := f.GetString("by")
		service, _ := f.GetString("service")
		kinds, _ := f.GetStringArray("kind")
		q, err := statsQuery(since, until, by, service, kinds, now)
		if err != nil {
			return err
		}
		var scope stats.Scope
		// Only owner and domain need the fleet: no cluster or registry read
		// for the other groupings.
		if by == "owner" || by == "domain" {
			fq, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			scope = stats.Scope{Place: stats.FleetPlacement(fq, nil), Snapshot: statsAttribution(fq)}
		}
		st, err := svc.Stats(cmd.Context(), app.StatsRequest{Registry: repo, Query: q, Scope: scope, Now: now})
		if err != nil {
			return err
		}
		return printStats(cmd, st, v.GetString(outputFormatKey))
	}
}
