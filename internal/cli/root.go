package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
)

const outputFormatKey = "output-format"

// NewRootCommand constructs the Cobra command tree with the given app service.
func NewRootCommand(svc *app.Service, version string) *cobra.Command {
	v := viper.New()

	root := &cobra.Command{
		Use:           "pacto",
		Short:         "Pacto — service contract standard for cloud-native services",
		Long:          "Pacto is a CLI tool for managing OCI-distributed service contracts.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Persistent flags
	root.PersistentFlags().String("config", "", "config file path")
	root.PersistentFlags().String(outputFormatKey, "text", "output format (text, json)")

	// Bind to Viper
	_ = v.BindPFlag("config", root.PersistentFlags().Lookup("config"))
	_ = v.BindPFlag(outputFormatKey, root.PersistentFlags().Lookup(outputFormatKey))

	// Env prefix
	v.SetEnvPrefix("PACTO")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	// Config file search
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		cfgFile := v.GetString("config")
		if cfgFile != "" {
			v.SetConfigFile(cfgFile)
		} else {
			v.SetConfigName("pacto")
			v.SetConfigType("yaml")
			v.AddConfigPath(".")
			v.AddConfigPath("$HOME/.config/pacto")
		}
		// Read config silently — it's optional
		_ = v.ReadInConfig()
		return nil
	}

	// Register subcommands
	root.AddCommand(newInitCommand(svc, v))
	root.AddCommand(newValidateCommand(svc, v))
	root.AddCommand(newDiffCommand(svc, v))
	root.AddCommand(newPackCommand(svc, v))
	root.AddCommand(newPushCommand(svc, v))
	root.AddCommand(newPullCommand(svc, v))
	root.AddCommand(newGraphCommand(svc, v))
	root.AddCommand(newExplainCommand(svc, v))
	root.AddCommand(newDocCommand(svc, v))
	root.AddCommand(newGenerateCommand(svc, v))
	root.AddCommand(newLoginCommand())
	root.AddCommand(newVersionCommand(version))

	return root
}
