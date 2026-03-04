package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newExplainCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explain [dir | oci://ref]",
		Short: "Human-readable contract summary",
		Long:  "Parses a pacto.yaml in the given directory (or oci:// reference) and produces a human-readable summary of the service contract.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			if len(args) > 0 {
				path = args[0]
			}

			result, err := svc.Explain(cmd.Context(), app.ExplainOptions{
				Path: path,
			})
			if err != nil {
				return err
			}

			format := v.GetString("output-format")
			return printExplainResult(cmd, result, format)
		},
	}

	return cmd
}
