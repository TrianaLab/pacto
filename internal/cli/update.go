package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/internal/update"
)

func newUpdateCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "update [version]",
		Short: "Update pacto to a newer version",
		Long:  "Downloads and installs the specified version of pacto. If no version is given, updates to the latest release.",
		Example: `  # Update to the latest release
  pacto update

  # Update to a specific version
  pacto update v1.1.0`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if version == "dev" {
				return fmt.Errorf("cannot update a dev build; install a release build from https://github.com/TrianaLab/pacto/releases")
			}

			targetVersion := ""
			if len(args) == 1 {
				targetVersion = args[0]
			}

			_, _ = fmt.Fprintln(cmd.OutOrStderr(), "Checking for updates...")

			result, err := update.Update(version, targetVersion)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated pacto %s -> %s\n", result.PreviousVersion, result.NewVersion)
			return nil
		},
	}
}
