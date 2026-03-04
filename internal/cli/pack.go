package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newPackCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pack [dir]",
		Short: "Create a bundle archive from a contract",
		Long:  "Validates the contract in the given directory and creates a tar.gz archive of the bundle, ready for distribution.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) > 0 {
				path = args[0]
			}

			output, _ := cmd.Flags().GetString("output")

			result, err := svc.Pack(cmd.Context(), app.PackOptions{
				Path:   path,
				Output: output,
			})
			if err != nil {
				return err
			}

			format := v.GetString("output-format")
			return printPackResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("output", "o", "", "output file path (default: <name>-<version>.tar.gz)")

	return cmd
}
