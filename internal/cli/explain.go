package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v4/internal/app"
)

func newExplainCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	var environments []string
	cmd := &cobra.Command{
		Use:   "explain [dir | oci://ref]",
		Short: "Human-readable contract summary",
		Long: "Parses a pacto.yaml in the given directory (or oci:// reference) and produces a human-readable summary of the service contract. " +
			"Each configuration lists its effective values per environment: its inline values with that environment's file merged on top. " +
			"With two or more environments it also lists the values they disagree on, for every environment or the ones --environments names.",
		Example: "  pacto explain my-service\n" +
			"  pacto explain oci://ghcr.io/acme/orders@sha256:<digest> --output-format json\n" +
			"  pacto explain my-service --environments test,production",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := optionalArg(args)

			result, err := svc.Explain(cmd.Context(), app.ExplainOptions{
				Path:         path,
				Overrides:    getOverrides(cmd),
				Environments: environments,
			})
			if err != nil {
				return err
			}

			format := v.GetString(outputFormatKey)
			return printExplainResult(cmd, result, format)
		},
	}

	addOverrideFlags(cmd)
	cmd.Flags().StringSliceVar(&environments, "environments", nil, "compare the effective configuration values of two or more environments, such as test,production")

	return cmd
}
