package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// VersionInfo holds build-time version metadata.
type VersionInfo struct {
	Version   string
	GitCommit string
	BuildDate string
}

func newVersionCommand(info VersionInfo) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print version information",
		Long:    "Prints the current pacto version.",
		Example: "  pacto version",
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(w, "Pacto:                %s\n", info.Version)
			_, _ = fmt.Fprintf(w, "Git Commit:           %s\n", info.GitCommit)
			_, _ = fmt.Fprintf(w, "Build Date:           %s\n", info.BuildDate)
			_, _ = fmt.Fprintf(w, "Go OS/Arch:           %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}
