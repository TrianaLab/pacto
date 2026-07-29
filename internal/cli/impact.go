package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
)

// newImpactCommand builds `pacto impact <old> <new>`: it composes a semantic
// contract diff with the operational graph to report a change's real blast
// radius. Source flags mirror `pacto fleet`; override flags mirror `pacto diff`.
func newImpactCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "impact <old> <new>",
		Short: "Analyze the blast radius of a change across the fleet",
		Long: "Composes a semantic contract diff (old→new) with the operational " +
			"graph to answer what a change's real blast radius is: which consumers " +
			"are affected, how strong the evidence is and whether their declared " +
			"compatibility still holds.\n\n" +
			"Exit status is non-zero when the change is BREAKING and at least one " +
			"active consumer is incompatible with the new version (mirrors `pacto diff`).",
		Example: `  # Impact of upgrading a local service against the local fleet
  pacto impact ./svc-v1 ./svc-v2 --local .

  # Include observed (runtime) evidence and emit JSON
  pacto impact oci://ghcr.io/acme/svc:1.0.0 ./svc --include-observed --output-format json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldOverrides, newOverrides := getDiffOverrides(cmd)
			includeObserved, _ := cmd.Flags().GetBool("include-observed")
			format := v.GetString(outputFormatKey)

			start := time.Now()
			sp := startSpinner(cmd, format, "Analyzing impact")
			result, err := svc.Impact(cmd.Context(), app.ImpactOptions{
				OldPath:         args[0],
				NewPath:         args[1],
				OldOverrides:    oldOverrides,
				NewOverrides:    newOverrides,
				Fleet:           fleetOptions(cmd),
				IncludeObserved: includeObserved,
			})
			if err != nil {
				sp.Stop()
				return err
			}
			sp.StopOK("Analyzed", start)

			err = printImpactResult(cmd, result, format)
			// Fail closed: a breaking change landing on a live, incompatible
			// consumer is a release blocker (mirrors `pacto diff`).
			if err == nil && result.Classification == "BREAKING" && hasIncompatibleActiveConsumer(result) {
				err = fmt.Errorf("breaking changes affect active consumers")
			}
			return err
		},
	}

	// Fleet source flags (mirrors `pacto fleet`).
	cmd.Flags().StringArray("local", []string{"."}, "local bundle root(s) to scan (repeatable)")
	cmd.Flags().StringArray("target-state", nil, "offline target-state fixture file(s) supplying targets (repeatable)")
	cmd.Flags().Duration("freshness", 0, "mark target evidence older than this as stale (0 disables)")
	cmd.Flags().Bool("include-observed", false, "let observed (runtime) relationships raise consumer confidence")
	addDiffOverrideFlags(cmd)

	return cmd
}

// hasIncompatibleActiveConsumer reports whether any affected consumer both
// declares an incompatible range and is actually deployed (has active targets).
func hasIncompatibleActiveConsumer(r *impact.Result) bool {
	for _, c := range r.Consumers {
		if c.CompatibilityVerdict == impact.CompatibilityIncompatible && len(c.Targets) > 0 {
			return true
		}
	}
	return false
}

// printImpactResult renders an impact result as text or JSON.
func printImpactResult(cmd *cobra.Command, result *impact.Result, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "Impact: %s %s -> %s\n", result.Service, orDash(result.OldVersion), orDash(result.NewVersion))
		_, _ = fmt.Fprintf(w, "Classification: %s\n", colorClassification(w, result.Classification))
		_, _ = fmt.Fprintf(w, "Breaking changes: %d\n", len(result.BreakingChanges))

		if len(result.Consumers) == 0 {
			_, _ = fmt.Fprintln(w, "No affected consumers.")
		} else {
			_, _ = fmt.Fprintf(w, "Affected consumers (%d):\n", len(result.Consumers))
			for _, c := range result.Consumers {
				scope := "transitive"
				if c.Direct {
					scope = "direct"
				}
				_, _ = fmt.Fprintf(w, "  %-28s %-10s confidence=%-12s compat=%-12s owner=%s\n",
					c.Service, scope, c.Confidence, c.CompatibilityVerdict, orDash(c.Owner))
			}
		}

		if len(result.ActiveTargets) > 0 {
			_, _ = fmt.Fprintf(w, "Active targets (%d): %v\n", len(result.ActiveTargets), result.ActiveTargets)
		}

		// The impact result carries completeness inline (not a fleet.Meta), so
		// wrap it to reuse the shared partial-answer warning.
		warnPartial(cmd, fleet.Meta{
			Completeness: result.Completeness,
			AsOf:         result.AsOf,
			Limitations:  result.Limitations,
		})
		return nil
	}, nil)
}
