package cli

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/logger"
	"github.com/trianalab/pacto/internal/update"
)

const outputFormatKey = "output-format"

// checkForUpdateFn is the function used to check for updates, overridable in tests.
var checkForUpdateFn = update.CheckForUpdate

// NewRootCommand constructs the Cobra command tree with the given app service.
func NewRootCommand(svc *app.Service, info VersionInfo) *cobra.Command {
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
	root.PersistentFlags().String(outputFormatKey, "text", "output format (text, json, markdown)")
	root.PersistentFlags().Bool("no-cache", false, "disable OCI bundle cache")
	root.PersistentFlags().BoolP("verbose", "v", false, "enable verbose output")

	// Bind to Viper
	_ = v.BindPFlag("config", root.PersistentFlags().Lookup("config"))
	_ = v.BindPFlag(outputFormatKey, root.PersistentFlags().Lookup(outputFormatKey))
	_ = v.BindPFlag("no-cache", root.PersistentFlags().Lookup("no-cache"))
	_ = v.BindPFlag("verbose", root.PersistentFlags().Lookup("verbose"))

	// Env prefix
	v.SetEnvPrefix("PACTO")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	// Channel for async update check result
	updateResultCh := make(chan *update.CheckResult, 1)

	// Config file search + async update check
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		logger.Setup(cmd.OutOrStderr(), v.GetBool("verbose"))

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

		if v.GetBool("no-cache") {
			if toggler, ok := svc.BundleStore.(interface{ DisableCache() }); ok {
				toggler.DisableCache()
			}
		}

		// Start async update check
		if info.Version != "dev" && os.Getenv("PACTO_NO_UPDATE_CHECK") != "1" {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Debug("update check panicked", "panic", r)
						updateResultCh <- nil
					}
				}()
				updateResultCh <- checkForUpdateFn(info.Version)
			}()
		} else {
			updateResultCh <- nil
		}

		return nil
	}

	// Post-run: show update notification if available
	root.PersistentPostRunE = func(cmd *cobra.Command, args []string) error {
		// Wait briefly for async check to complete (cache reads are near-instant)
		select {
		case result := <-updateResultCh:
			if result != nil && cmd.Name() != "update" && v.GetString(outputFormatKey) == "text" {
				_, _ = fmt.Fprintf(cmd.OutOrStderr(), "\nA new version of pacto is available: %s -> %s\nRun 'pacto update' to update.\n", result.CurrentVersion, result.LatestVersion)
			}
		case <-time.After(200 * time.Millisecond):
			// Check took too long (likely a network fetch) — don't delay the user
		}
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
	root.AddCommand(newVersionCommand(info))
	root.AddCommand(newUpdateCommand(info.Version))
	root.AddCommand(newMCPCommand(svc, info.Version))
	root.AddCommand(newDashboardCommand(svc, v))

	return root
}
