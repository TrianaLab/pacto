package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/otelobserver"
)

// newOTelCommand groups OpenTelemetry-backed observation tools. The observer is
// a producer of evidence, not an authority: it reports what traffic it saw and
// never asserts a dependency is absent.
func newOTelCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "otel",
		Short: "Observe runtime dependencies from OpenTelemetry traces",
	}
	cmd.AddCommand(newOTelObserveCommand(svc, v))
	return cmd
}

func newOTelObserveCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "observe <traces.json>",
		Short: "Derive observed service dependencies from OTLP/JSON traces",
		Long: "Reads an OTLP/JSON trace export and derives the service dependency " +
			"edges its outbound spans prove. By default it prints the observed " +
			"edges as text; add --output-format json for machine-readable output. " +
			"With --evidence it emits one EvidenceSet per calling service -- a JSON " +
			"array, and each set's ContractRef is empty because traces do not name a " +
			"contract revision. Signing is therefore not a pipe: pacto evidence sign " +
			"reads one EvidenceSet from a file, so write the array out, split it, and " +
			"set each ContractRef to the revision it describes before signing " +
			"(pacto evidence sign) and reporting (pacto evidence send).",
		Example: `  pacto otel observe traces.json
  pacto otel observe traces.json --evidence --output-format json > sets.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			emitEvidence, _ := cmd.Flags().GetBool("evidence")
			source, _ := cmd.Flags().GetString("source")
			format := v.GetString(outputFormatKey)

			edges, sets, err := svc.ObserveOTel(data, otelobserver.Options{
				Collector:  source,
				ObservedAt: time.Now().UTC(),
			})
			if err != nil {
				return err
			}
			if emitEvidence {
				return printObservedEvidence(cmd, sets, format)
			}
			return printObservedEdges(cmd, edges, format)
		},
	}
	cmd.Flags().Bool("evidence", false, "emit signable EvidenceSets instead of raw edges")
	cmd.Flags().String("source", "otel", "collector/source name recorded as provenance")
	return cmd
}

func printObservedEdges(cmd *cobra.Command, edges []otelobserver.Edge, format string) error {
	return formatResult(cmd, format, edges, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "Observed dependencies (%d):\n", len(edges))
		for _, e := range edges {
			_, _ = fmt.Fprintf(w, "  %s -> %s (count=%d)\n", e.From, e.To, e.Count)
		}
		return nil
	}, nil)
}

func printObservedEvidence(cmd *cobra.Command, sets []evidence.EvidenceSet, format string) error {
	return formatResult(cmd, format, sets, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "EvidenceSets (%d):\n", len(sets))
		for _, s := range sets {
			_, _ = fmt.Fprintf(w, "  %s: %d observed dependencies\n", s.Subject.Name, len(s.Observations))
		}
		return nil
	}, nil)
}
