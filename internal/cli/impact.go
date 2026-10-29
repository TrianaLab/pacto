package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// newImpactCommand builds `pacto impact <old> <new>`: it composes a semantic
// contract diff with the operational graph to report a change's real blast
// radius. Source flags mirror `pacto fleet`; override flags mirror `pacto diff`.
func newImpactCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "impact <old> <new>",
		Short: "Analyze which consumers a change affects across the fleet",
		Long: "Composes a semantic contract diff (old→new) with the operational " +
			"graph to answer which consumers a change really affects, how strong " +
			"the evidence is and whether their declared compatibility still " +
			"holds.\n\n" +
			"When no fleet source holds the changed service, the new revision is added " +
			"to the snapshot so the services that depend on it are found.\n\n" +
			"The answer ends in a release gate, and the exit status follows it:\n" +
			"  0  clear: the change is not BREAKING, or the snapshot is complete and no service depends on the changed one.\n" +
			"  1  blocked: the change is BREAKING and a consumer that does not accept the new version runs on a known target.\n" +
			"  2  unverified: the change is BREAKING and nothing is shown to block it, but the answer cannot clear it: " +
			"the answer is partial, or consumers depend on the service and none is shown to be safe. " +
			"A run over --local bundles alone knows no targets, so a BREAKING change with consumers exits 2.\n" +
			"A command that could not run also exits 1, with nothing on stdout. Add --target-state " +
			"(or a live source) so the gate can tell deployed impact apart.\n\n" +
			"Pacto finds consumers only in the sources you pass, such as --local, --root, " +
			"--oci, --cache, --k8s, --evidence-url and --traces. With --local alone " +
			"Pacto sees only the contracts in those folders, so \"No affected " +
			"consumers\" is not evidence of safety.",
		Example: `  # Impact of upgrading a local service against the local fleet
  pacto impact ./svc-v1 ./svc-v2 --local .

  # Include observed (runtime) evidence and emit JSON
  pacto impact oci://ghcr.io/acme/svc:1.0.0 ./svc --include-observed --output-format json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldOverrides, newOverrides := getDiffOverrides(cmd)
			includeObserved, _ := cmd.Flags().GetBool("include-observed")
			format := v.GetString(outputFormatKey)

			var traces []byte
			if tracesPath, _ := cmd.Flags().GetString("traces"); tracesPath != "" {
				var readErr error
				if traces, readErr = os.ReadFile(tracesPath); readErr != nil {
					return readErr
				}
				includeObserved = true // supplying traces implies observed analysis
			}

			start := time.Now()
			sp := startSpinner(cmd, format, "Analyzing impact")
			result, err := svc.Impact(cmd.Context(), app.ImpactOptions{
				OldPath:         args[0],
				NewPath:         args[1],
				OldOverrides:    oldOverrides,
				NewOverrides:    newOverrides,
				Fleet:           fleetOptions(cmd),
				IncludeObserved: includeObserved,
				Traces:          traces,
			})
			if err != nil {
				sp.Stop()
				return err
			}
			sp.StopOK("Analyzed", start)

			if err := printImpactResult(cmd, result, format); err != nil {
				return err
			}
			// Fail closed. A blocked change exits 1, as `pacto diff` does on a
			// breaking change. An unverified one exits 2, so a CI job never reads
			// "could not tell" as "safe", while a promotion job that gates on
			// deployed impact alone can still tell the two apart.
			switch result.Gate {
			case impact.GateBlocked:
				return errors.New(result.GateReason)
			case impact.GateUnverified:
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "unverified: "+result.GateReason)
				return &ExitError{Code: 2}
			}
			return nil
		},
	}

	cmd.Flags().Bool("include-observed", false, "let observed (runtime) relationships raise consumer confidence")
	// Declared before the shared set so this narrowing wins: impact takes ONE
	// trace document and reads it itself (see divergentFleetFlagTypes).
	cmd.Flags().String("traces", "", "OTLP/JSON trace file; its observed edges corroborate and surface consumers (implies --include-observed)")
	// Every other fleet source flag, from the one declaration `pacto fleet` uses.
	addFleetSourceFlags(cmd.Flags())
	addDiffOverrideFlags(cmd)

	return cmd
}

// printImpactResult renders an impact result as text or JSON.
func printImpactResult(cmd *cobra.Command, result *impact.Result, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "Impact: %s %s -> %s\n", result.Service, orDash(result.OldVersion), orDash(result.NewVersion))
		_, _ = fmt.Fprintf(w, "Classification: %s\n", colorClassification(w, result.Classification))
		_, _ = fmt.Fprintf(w, "Breaking changes: %d\n", len(result.BreakingChanges))
		_, _ = fmt.Fprintf(w, "Potentially breaking changes: %d\n", len(result.PotentiallyBreakingChanges))
		if result.NewRevisionAdded {
			_, _ = fmt.Fprintf(w, "%s is in none of the fleet sources; the new revision was added to the snapshot to find its consumers.\n", result.Service)
		}

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
				if c.Note != "" {
					_, _ = fmt.Fprintf(w, "    note: %s\n", c.Note)
				}
			}
		}

		if len(result.ActiveTargets) > 0 {
			_, _ = fmt.Fprintf(w, "Active targets (%d): %v\n", len(result.ActiveTargets), result.ActiveTargets)
		}
		_, _ = fmt.Fprintf(w, "Release gate: %s\n", result.Gate)

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
