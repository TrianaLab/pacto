package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
)

func newMigrateCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate [dir...]",
		Short: "Raise every pacto.yaml in a repository to the current pactoVersion",
		Long: `Walks the given directories (the current one by default) with the rules of the local fleet source and raises the pactoVersion of every pacto.yaml it finds. Hidden folders, node_modules and vendor are skipped; folders past depth 8 and folders a root .pactoignore names are skipped and printed as skipped.

A file is raised only when validation finds nothing new at the new version. Its version token changes, and two removals can apply even to a file already at the version: a leftover delivery section, and any publish.artifacts[].build block that pacto ci --publish detection reproduces. Every other byte stays as written, and each line names what changed. A raise that would change what the contract means keeps the file and says why. 1.x files are left to MIGRATION.md.

Exits 1 only when a file cannot be read or written, or --to names an unsupported version.`,
		Example: `  pacto migrate
  pacto migrate --dry-run
  pacto migrate services/payments
  pacto migrate --to 2.4`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			to, _ := cmd.Flags().GetString("to")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			result, err := svc.Migrate(cmd.Context(), app.MigrateOptions{Roots: args, To: to, DryRun: dryRun})
			if err != nil {
				return err
			}
			var failed error
			if result.Failed > 0 {
				failed = &ExitError{Code: 1, Reason: fmt.Sprintf("%d pacto.yaml file(s) or folder(s) could not be read or written", result.Failed)}
			}
			if v.GetString(outputFormatKey) == "json" {
				return printJSONExit(cmd, result, failed)
			}
			printMigrateResult(cmd, result)
			return failed
		},
	}
	cmd.Flags().String("to", "", "pactoVersion to raise to (default: the newest this Pacto supports)")
	cmd.Flags().Bool("dry-run", false, "print what would change and write nothing")
	return cmd
}

// printMigrateResult prints one line per file, skipped folder and root with
// no pacto.yaml, padded to the longest path, then the summary. A message that
// spans lines (a YAML parse error) is folded onto one.
func printMigrateResult(cmd *cobra.Command, r *app.MigrateResult) {
	type line struct{ path, msg string }
	var lines []line
	for _, f := range r.Files {
		lines = append(lines, line{f.Path, strings.Join(strings.Fields(f.Message), " ")})
	}
	for _, s := range r.SkippedDirs {
		lines = append(lines, line{s.Path, "skipped (from " + s.Rule + ")"})
	}
	for _, root := range r.NotFound {
		lines = append(lines, line{root, "no pacto.yaml found"})
	}
	width := 0
	for _, l := range lines {
		width = max(width, len(l.path)+1)
	}
	w := cmd.OutOrStdout()
	for _, l := range lines {
		_, _ = fmt.Fprintf(w, "%-*s %s\n", width, l.path+":", l.msg)
	}
	_, _ = fmt.Fprintln(w, r.Summary())
}
