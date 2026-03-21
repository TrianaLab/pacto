package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newPushCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push <ref>",
		Short: "Push a contract bundle to an OCI registry",
		Long:  "Validates the contract, builds an OCI image, and pushes it to the specified registry reference.",
		Example: `  # Push with auto-tag (uses contract version)
  pacto push oci://ghcr.io/acme/my-service-pacto -p my-service

  # Push with explicit tag
  pacto push oci://ghcr.io/acme/my-service-pacto:latest -p my-service

  # Force overwrite an existing artifact
  pacto push oci://ghcr.io/acme/my-service-pacto -p my-service --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			path, _ := cmd.Flags().GetString("path")
			force, _ := cmd.Flags().GetBool("force")

			result, err := svc.Push(cmd.Context(), app.PushOptions{
				Ref:       ref,
				Path:      path,
				Force:     force,
				Overrides: getOverrides(cmd),
			})
			if err != nil {
				if errors.Is(err, app.ErrArtifactAlreadyExists) {
					_, _ = fmt.Fprintf(cmd.OutOrStderr(), "Warning: %s\n", err)
					return nil
				}
				return err
			}

			format := v.GetString(outputFormatKey)
			return printPushResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("path", "p", "", "path to contract directory (default: current directory)")
	cmd.Flags().BoolP("force", "f", false, "overwrite existing artifact in registry")

	addOverrideFlagsNoShorthand(cmd)

	return cmd
}
