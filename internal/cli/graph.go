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
			var path string
			if len(args) > 0 {
				path = args[0]
			}

			result, err := svc.Graph(cmd.Context(), app.GraphOptions{
				Path: path,
			})
			if err != nil {
				return err
			}

			format := v.GetString(outputFormatKey)
			return printGraphResult(cmd, result, format)
		},
	}

	return cmd
}
