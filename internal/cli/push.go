package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v2/internal/app"
)

func newPushCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push <ref>",
		Short: "Push a contract bundle to an OCI registry",
		Long:  "Validates the contract (including remote policy and config refs), builds an OCI artifact, and pushes it to the specified registry reference.",
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
			format := v.GetString(outputFormatKey)

			start := time.Now()
			sp := startSpinner(cmd, format, "Pushing "+ref)
			result, err := svc.Push(cmd.Context(), app.PushOptions{
				Ref:       ref,
				Path:      path,
				Force:     force,
				Overrides: getOverrides(cmd),
			})
			if err != nil {
				sp.Stop()
				if errors.Is(err, app.ErrArtifactAlreadyExists) {
					_, _ = fmt.Fprintf(cmd.OutOrStderr(), "Warning: %s\n", err)
					return nil
				}
				return err
			}
			sp.StopOK("Pushed", start)

			return printPushResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("path", "p", "", "path to contract directory (default: current directory)")
	cmd.Flags().BoolP("force", "f", false, "overwrite existing artifact in registry")

	addOverrideFlagsNoShorthand(cmd)

	return cmd
}
