package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newValidateCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [dir | oci://ref]",
		Short: "Validate a pacto contract",
		Long:  "Validates a pacto.yaml in the given directory (or oci:// reference) against the specification, checking structural, cross-field, and semantic rules.",
		Example: `  # Validate a local contract
  pacto validate my-service

  # Validate from current directory
  pacto validate

  # Validate from an OCI registry
  pacto validate oci://ghcr.io/acme/my-service-pacto:1.0.0

  # JSON output
  pacto validate --output-format json my-service`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := optionalArg(args)

			result, err := svc.Validate(cmd.Context(), app.ValidateOptions{
				Path:      path,
				Overrides: getOverrides(cmd),
			})
			if err != nil {
				return err
			}

			format := v.GetString(outputFormatKey)
			if err := printValidateResult(cmd, result, format); err != nil {
				return err
			}

			if !result.Valid {
				return fmt.Errorf("validation failed with %d error(s)", len(result.Errors))
			}

			return nil
		},
	}

	addOverrideFlags(cmd)

	return cmd
}
