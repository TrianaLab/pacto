package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newExplainCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "explain [dir | oci://ref]",
		Short:   "Human-readable contract summary",
		Long:    "Parses a pacto.yaml in the given directory (or oci:// reference) and produces a human-readable summary of the service contract.",
		Example: "  pacto explain my-service",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := optionalArg(args)

			result, err := svc.Explain(cmd.Context(), app.ExplainOptions{
				Path:      path,
				Overrides: getOverrides(cmd),
			})
			if err != nil {
				return err
			}

			format := v.GetString(outputFormatKey)
			return printExplainResult(cmd, result, format)
		},
	}

	addOverrideFlags(cmd)

	return cmd
}
