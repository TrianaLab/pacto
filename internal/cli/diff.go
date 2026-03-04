package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newDiffCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <old> <new>",
		Short: "Compare two contracts and classify changes",
		Long:  "Compares two contracts (local paths or oci:// references) and produces a classified change set (BREAKING, POTENTIAL_BREAKING, NON_BREAKING).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := svc.Diff(cmd.Context(), app.DiffOptions{
				OldPath: args[0],
				NewPath: args[1],
			})
			if err != nil {
				return err
			}

			format := v.GetString(outputFormatKey)
			if err := printDiffResult(cmd, result, format); err != nil {
				return err
			}

			if result.Classification == "BREAKING" {
				return fmt.Errorf("breaking changes detected")
			}

			return nil
		},
	}

	return cmd
}
