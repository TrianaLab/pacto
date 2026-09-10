package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

func newLogoutCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "logout <registry>",
		Short:   "Remove stored credentials for an OCI registry",
		Long:    "Removes credentials for an OCI registry from ~/.config/pacto/config.json.",
		Example: "  pacto logout ghcr.io",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := args[0]

			// Same writer as `pacto login`: pkg/oci owns the credential file, so
			// the removal fails closed on a config it cannot read and leaves the
			// rewritten file mode 0600.
			removed, err := oci.RemoveCredential(registry)
			if err != nil {
				return err
			}

			if removed {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Logout succeeded for %s\n", registry)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No stored credentials for %s\n", registry)
			}

			return nil
		},
	}

	return cmd
}
