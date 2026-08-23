package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v3/internal/app"
)

func newValidateCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [dir | oci://ref]",
		Short: "Validate a pacto contract",
		Long:  "Validates a pacto.yaml in the given directory (or oci:// reference) against the specification, running the three validation layers: structural, cross-field, and policy.",
		Example: `  # Validate a local contract
  pacto validate my-service

  # Validate from current directory
  pacto validate

  # Validate from an OCI registry
  pacto validate oci://ghcr.io/acme/my-service-pacto:1.0.0

  # JSON output
  pacto validate --output-format json my-service

  # Also enforce the readiness gate (fail if score < minScore)
  pacto validate --readiness my-service`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := optionalArg(args)
			checkReadiness, _ := cmd.Flags().GetBool("readiness")
			format := v.GetString(outputFormatKey)

			sp := startSpinner(cmd, format, "Validating")
			result, err := svc.Validate(cmd.Context(), app.ValidateOptions{
				Path:      path,
				Overrides: getOverrides(cmd),
				Readiness: checkReadiness,
			})
			sp.Stop()
			if err != nil {
				return err
			}

			if err := printValidateResult(cmd, result, format); err != nil {
				return err
			}

			if !result.Valid {
				return fmt.Errorf("validation failed with %d error(s)", len(result.Errors))
			}

			return nil
		},
	}

	cmd.Flags().Bool("readiness", false, "also enforce the readiness gate: fail if the derived readiness score is below the declared (or default 100) minScore. Opt-in because gate evaluation is time-dependent (check expiry is compared against the run time), which would otherwise make plain validation non-deterministic")
	addOverrideFlags(cmd)

	return cmd
}
