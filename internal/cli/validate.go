package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v4/internal/app"
)

func newValidateCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [dir | oci://ref]",
		Short: "Validate a pacto contract",
		Long: "Validates a pacto.yaml in the given directory (or oci:// reference) against the specification, running the three validation layers: structural, cross-field and policy.\n\n" +
			"The output has one PASS, FAIL or SKIP line per stage, in order: load, lock, structural, cross-field, policy, environments, guarantees, readiness and closure. " +
			"The environments stage checks each environment's values file against the schema of every configuration it names. " +
			"Each error sits under its stage with its file:line:column when Pacto can locate it. A failed load or lock stage skips every stage after it, and a failed structural or cross-field stage skips the stages after it up to policy. Any failed stage skips environments. The guarantees, readiness and closure stages run even when an earlier stage fails. " +
			"The last line, Next:, names the one command to run now. With --output-format json the same comes as stages and next.",
		Example: `  # Validate a local contract
  pacto validate my-service

  # Validate from current directory
  pacto validate

  # Validate from an OCI registry
  pacto validate oci://ghcr.io/acme/my-service-pacto:1.0.0

  # JSON output
  pacto validate --output-format json my-service

  # Also check the readiness gate (fail if score < minScore)
  pacto validate --readiness my-service

  # Also check every dependency against this contract's policies
  pacto validate --closure my-service`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := optionalArg(args)
			checkReadiness, _ := cmd.Flags().GetBool("readiness")
			closure, _ := cmd.Flags().GetBool("closure")
			format := v.GetString(outputFormatKey)

			sp := startSpinner(cmd, format, "Validating")
			result, err := svc.Validate(cmd.Context(), app.ValidateOptions{
				Path:      path,
				Overrides: getOverrides(cmd),
				Readiness: checkReadiness,
				Closure:   closure,
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

	cmd.Flags().Bool("readiness", false, "also check the readiness gate: fail if the derived readiness score is below the declared (or default 100) minScore. Opt-in because gate evaluation is time-dependent (check expiry is compared against the run time), which would otherwise make plain validation non-deterministic")
	cmd.Flags().Bool("closure", false, "also check every contract in the dependency closure against the policies this contract resolves. Fails when a dependency cannot be resolved and warns about deprecated interfaces of direct dependencies. Fetches each dependency once")
	addOverrideFlags(cmd)

	return cmd
}
