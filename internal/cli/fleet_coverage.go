package cli

import (
	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// addCoverageFlags adds pacto fleet status's two coverage record targets.
// They have their own help: unlike the run flags, they never read the env.
func addCoverageFlags(cmd *cobra.Command) {
	cmd.Flags().String("stats-registry", "", "also write a coverage run record to this stats repository (unsigned statistics); never read from PACTO_STATS_REGISTRY")
	cmd.Flags().String("stats-file", "", "also write the coverage run record to this file, for pacto stats publish in another job; never read from PACTO_STATS_FILE")
}

// recordCoverage writes the coverage record of q to the targets named on the
// command line, best effort. The derivation is stats.CoverageOf, shared with
// the dashboard.
func recordCoverage(cmd *cobra.Command, rec *runRecorder, q *fleet.Query) {
	if reg, file := flagTargets(cmd); reg == "" && file == "" {
		return
	}
	r := runrecord.CoverageRecord(stats.CoverageOf(q), rec.version, rec.now())
	rec.saveFlagsOnly(cmd, &r)
}
