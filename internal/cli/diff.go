package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v3/internal/app"
)

func newDiffCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <old> <new>",
		Short: "Compare two contracts and classify changes",
		Long:  "Compares two contracts (local paths or oci:// references) and produces a classified change set (BREAKING, POTENTIAL_BREAKING, NON_BREAKING).",
		Example: `  # Compare a remote contract with a local one
  pacto diff oci://ghcr.io/acme/svc-pacto:1.0.0 my-service

  # Markdown output for CI comments
  pacto diff --output-format markdown oci://ghcr.io/acme/svc-pacto:1.0.0 my-service`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldOverrides, newOverrides := getDiffOverrides(cmd)
			format := v.GetString(outputFormatKey)

			start := time.Now()
			sp := startSpinner(cmd, format, "Resolving diff")
			result, err := svc.Diff(cmd.Context(), app.DiffOptions{
				OldPath:      args[0],
				NewPath:      args[1],
				OldOverrides: oldOverrides,
				NewOverrides: newOverrides,
			})
			if err != nil {
				sp.Stop()
				return err
			}
			sp.StopOK("Diffed", start)

			if err := printDiffResult(cmd, result, format); err != nil {
				return err
			}

			if result.Classification == "BREAKING" {
				return fmt.Errorf("breaking changes detected")
			}

			return nil
		},
	}

	addDiffOverrideFlags(cmd)

	return cmd
}
