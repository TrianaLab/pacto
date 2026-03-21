package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newGraphCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "graph [dir | oci://ref]",
		Short:   "Resolve and display the dependency graph",
		Long:    "Resolves the dependency tree from a pacto.yaml in the given directory (or oci:// reference) and displays the graph, cycles, and version conflicts.",
		Example: "  pacto graph my-service",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := optionalArg(args)

			result, err := svc.Graph(cmd.Context(), app.GraphOptions{
				Path:      path,
				Overrides: getOverrides(cmd),
			})
			if err != nil {
				return err
			}

			format := v.GetString(outputFormatKey)
			return printGraphResult(cmd, result, format)
		},
	}

	addOverrideFlags(cmd)

	return cmd
}
