package cli

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// VersionInfo holds build-time version metadata.
type VersionInfo struct {
	Version   string
	GitCommit string
	BuildDate string
}

func newVersionCommand(info VersionInfo, v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print version information",
		Long:    "Prints the current pacto version.",
		Example: "  pacto version",
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			out := struct {
				Version   string `json:"version"`
				GitCommit string `json:"gitCommit"`
				BuildDate string `json:"buildDate"`
				OS        string `json:"os"`
				Arch      string `json:"arch"`
			}{info.Version, info.GitCommit, info.BuildDate, runtime.GOOS, runtime.GOARCH}

			switch v.GetString(outputFormatKey) {
			case "json":
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				_ = enc.Encode(out)
			case "markdown":
				_, _ = fmt.Fprintln(w, "| Field | Value |")
				_, _ = fmt.Fprintln(w, "|---|---|")
				_, _ = fmt.Fprintf(w, "| Pacto | %s |\n", out.Version)
				_, _ = fmt.Fprintf(w, "| Git Commit | %s |\n", out.GitCommit)
				_, _ = fmt.Fprintf(w, "| Build Date | %s |\n", out.BuildDate)
				_, _ = fmt.Fprintf(w, "| Go OS/Arch | %s/%s |\n", out.OS, out.Arch)
			default:
				_, _ = fmt.Fprintf(w, "Pacto:                %s\n", out.Version)
				_, _ = fmt.Fprintf(w, "Git Commit:           %s\n", out.GitCommit)
				_, _ = fmt.Fprintf(w, "Build Date:           %s\n", out.BuildDate)
				_, _ = fmt.Fprintf(w, "Go OS/Arch:           %s/%s\n", out.OS, out.Arch)
			}
		},
	}
}
