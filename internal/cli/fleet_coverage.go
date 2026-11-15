package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// addCoverageFlags adds pacto fleet status's two coverage record targets.
// They have their own help: unlike the run flags, they never read the env.
func addCoverageFlags(cmd *cobra.Command) {
	cmd.Flags().String("stats-registry", "", "also write a coverage run record of the whole fleet to this stats repository (unsigned statistics; none with --namespace); never read from PACTO_STATS_REGISTRY")
	cmd.Flags().String("stats-file", "", "also write the coverage run record to this file, for pacto stats publish in another job; never read from PACTO_STATS_FILE")
}

// recordCoverage writes the coverage record of q to the targets named on the
// command line, best effort. The derivation is stats.CoverageOf, shared with
// the dashboard. A coverage record counts the whole fleet, so a snapshot
// narrowed to one namespace records none: it would read as complete fleet
// coverage beside the whole fleet's.
func recordCoverage(cmd *cobra.Command, rec *runRecorder, q *fleet.Query) {
	if reg, file := flagTargets(cmd); reg == "" && file == "" {
		return
	}
	if ns, _ := cmd.Flags().GetString("namespace"); ns != "" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "pacto: warning: coverage run record not written: it counts the whole fleet, and --namespace reads one namespace")
		return
	}
	r := runrecord.CoverageRecord(stats.CoverageOf(q), rec.version, rec.now())
	rec.saveFlagsOnly(cmd, &r)
}
