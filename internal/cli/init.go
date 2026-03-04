package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

func newInitCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Scaffold a new pacto project",
		Long:  "Creates a new directory with pacto.yaml and the bundle directory structure (interfaces/, configuration/).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			result, err := svc.Init(cmd.Context(), app.InitOptions{
				Name: name,
			})
			if err != nil {
				return err
			}

			format := v.GetString(outputFormatKey)
			return printInitResult(cmd, result, format)
		},
	}

	return cmd
}
