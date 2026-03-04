package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newPushCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push <ref>",
		Short: "Push a contract bundle to an OCI registry",
		Long:  "Validates the contract, builds an OCI image, and pushes it to the specified registry reference.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			path, _ := cmd.Flags().GetString("path")

			result, err := svc.Push(cmd.Context(), app.PushOptions{
				Ref:  ref,
				Path: path,
			})
			if err != nil {
				return err
			}

			format := v.GetString("output-format")
			return printPushResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("path", "p", "", "path to contract directory (default: current directory)")

	return cmd
}
