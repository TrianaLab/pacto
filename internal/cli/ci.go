package cli

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/ci"
)

// newCICommand builds `pacto ci [root]`: plan every service of a repository
// for a CI run. It reads the registry and writes nothing.
func newCICommand(svc *app.Service, _ *viper.Viper, _ deps, _ *runRecorder) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci [root]",
		Short: "Plan every service of a repository for a CI run",
		Long: "Walks root (default .) for pacto.yaml, skipping the folders the root .pactoignore names, and " +
			"prints one plan: the CI platform, the registry and the base, each with its source, then one row per service.\n\n" +
			"The exit status follows the plan: 0 clear, 1 blocked, 2 unverified. A repository with no pacto.yaml exits 2.",
		Example: `  # Plan every service of this repository against a registry
  pacto ci --registry ghcr.io/acme`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			registry, _ := cmd.Flags().GetString("registry")
			p, _, err := svc.CIPlan(cmd.Context(), app.CIOptions{Root: root, Registry: registry, Fleet: fleetOptions(cmd), Getenv: os.Getenv})
			if err != nil {
				return err
			}
			ci.Text(cmd.OutOrStdout(), p)
			return exitStatus(p.Exit)
		},
	}
	cmd.Flags().String("registry", "", "registry the services publish to, for example ghcr.io/acme; default from the CI platform")
	addFleetSourceFlags(cmd.Flags())
	addStatsFlags(cmd)
	return cmd
}

// exitStatus is the error that ends the process with code, nil for 0.
func exitStatus(code int) error {
	if code == 0 {
		return nil
	}
	return &ExitError{Code: code}
}
