package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newGenerateCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	var options []string

	cmd := &cobra.Command{
		Use:   "generate <plugin> [dir | oci://ref]",
		Short: "Generate artifacts from a contract using a plugin",
		Long:  "Invokes a pacto-plugin-<name> binary to generate deployment manifests, documentation, or other artifacts from a contract directory or oci:// reference.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pluginName := args[0]
			var path string
			if len(args) > 1 {
				path = args[1]
			}

			outputDir, _ := cmd.Flags().GetString("output")

			result, err := svc.Generate(cmd.Context(), app.GenerateOptions{
				Path:      path,
				OutputDir: outputDir,
				Plugin:    pluginName,
				Options:   parseOptions(options),
			})
			if err != nil {
				return err
			}

			format := v.GetString("output-format")
			return printGenerateResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("output", "o", "", "output directory (default: <plugin>-output/)")
	cmd.Flags().StringArrayVar(&options, "option", nil, "plugin option as key=value (can be repeated)")

	return cmd
}

// parseOptions converts a slice of "key=value" strings into a map.
func parseOptions(options []string) map[string]any {
	if len(options) == 0 {
		return nil
	}
	m := make(map[string]any, len(options))
	for _, opt := range options {
		if k, v, ok := strings.Cut(opt, "="); ok {
			m[k] = v
		}
	}
	return m
}
