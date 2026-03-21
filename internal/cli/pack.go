package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newPackCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pack [dir]",
		Short:   "Create a bundle archive from a contract",
		Long:    "Validates the contract in the given directory and creates a tar.gz archive of the bundle, ready for distribution.",
		Example: "  pacto pack my-service",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := optionalArg(args)

			output, _ := cmd.Flags().GetString("output")

			result, err := svc.Pack(cmd.Context(), app.PackOptions{
				Path:      path,
				Output:    output,
				Overrides: getOverrides(cmd),
			})
			if err != nil {
				return err
			}

			format := v.GetString(outputFormatKey)
			return printPackResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("output", "o", "", "output file path (default: <name>-<version>.tar.gz)")

	addOverrideFlags(cmd)

	return cmd
}
