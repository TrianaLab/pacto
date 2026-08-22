package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/reconcile"
)

// newFleetReconcileCommand builds `pacto fleet reconcile`: declared dependencies
// come from the fleet sources (the shared --local/--k8s/... flags), observed
// ones from an OTLP/JSON trace file, and the report names where intent and
// reality agree or diverge.
func newFleetReconcileCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Compare declared dependencies against observed traffic",
		Long: "Reconciles what the fleet's contracts declare against what runtime " +
			"traces prove. Reports matched dependencies, declared-but-not-observed " +
			"(dormant or unseen — not proof of a dead dependency) and " +
			"observed-but-not-declared (shadow dependencies).",
		Example: `  pacto fleet reconcile --traces traces.json --local .`,
		RunE: func(cmd *cobra.Command, args []string) error {
			traces, _ := cmd.Flags().GetString("traces")
			data, err := os.ReadFile(traces)
			if err != nil {
				return err
			}
			report, err := svc.Reconcile(cmd.Context(), app.ReconcileOptions{
				Fleet:  fleetOptions(cmd),
				Traces: data,
			})
			if err != nil {
				return err
			}
			return printReconcileReport(cmd, report, v.GetString(outputFormatKey))
		},
	}
	cmd.Flags().String("traces", "", "OTLP/JSON trace file supplying observed dependencies (required)")
	_ = cmd.MarkFlagRequired("traces")
	return cmd
}

func printReconcileReport(cmd *cobra.Command, report reconcile.Report, format string) error {
	return formatResult(cmd, format, report, func() error {
		w := cmd.OutOrStdout()
		s := report.Summary
		_, _ = fmt.Fprintf(w, "Reconciliation: %d matched, %d declared-not-observed, %d observed-not-declared",
			s.Matched, s.DeclaredNotObserved, s.ObservedNotDeclared)
		if s.Unresolved > 0 {
			_, _ = fmt.Fprintf(w, ", %d unresolved", s.Unresolved)
		}
		_, _ = fmt.Fprintln(w)
		for _, e := range report.Entries {
			_, _ = fmt.Fprintf(w, "  [%s] %s -> %s (count=%d)\n", e.Status, e.Service, e.Dependency, e.Count)
		}
		for _, u := range report.Unresolved {
			_, _ = fmt.Fprintf(w, "  [unresolved:%s] %s -> %s (count=%d) — observed name not mapped to a unique fleet service\n",
				u.Reason, u.Service, u.Dependency, u.Count)
		}
		return nil
	}, nil)
}
