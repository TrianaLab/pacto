package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newPullCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull <ref>",
		Short: "Pull a contract bundle from an OCI registry",
		Long:  "Pulls a contract bundle from the specified OCI reference and extracts it to a local directory.",
		Example: `  # Pull a specific version
  pacto pull oci://ghcr.io/acme/my-service-pacto:1.0.0

  # Pull the latest available version
  pacto pull oci://ghcr.io/acme/my-service-pacto`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			output, _ := cmd.Flags().GetString("output")

			result, err := svc.Pull(cmd.Context(), app.PullOptions{
				Ref:    ref,
				Output: output,
			})
			if err != nil {
				return err
			}

			format := v.GetString(outputFormatKey)
			return printPullResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("output", "o", "", "output directory (default: service name)")

	return cmd
}
