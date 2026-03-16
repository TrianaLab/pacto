package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print version information",
		Long:    "Prints the current pacto version.",
		Example: "  pacto version",
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "pacto version %s\n", version)
		},
	}
}
