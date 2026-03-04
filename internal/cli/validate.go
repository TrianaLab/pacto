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
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) > 0 {
				path = args[0]
			}

			result, err := svc.Validate(cmd.Context(), app.ValidateOptions{
				Path: path,
			})
			if err != nil {
				return err
			}

			format := v.GetString("output-format")
			if err := printValidateResult(cmd, result, format); err != nil {
				return err
			}

			if !result.Valid {
				return fmt.Errorf("validation failed with %d error(s)", len(result.Errors))
			}

			return nil
		},
	}

	return cmd
}
