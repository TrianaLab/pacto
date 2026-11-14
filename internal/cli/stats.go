package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// errNoStatsRegistry is what every stats command says when it has nowhere to go.
var errNoStatsRegistry = errors.New("no stats registry: pass --registry or set PACTO_STATS_REGISTRY")

func newStatsCommand(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Work with Pacto's unsigned run statistics",
		Long:  "Run records are unsigned statistics about Pacto runs. Nothing that decides ever reads them.",
	}
	cmd.AddCommand(newStatsPublishCommand(d))
	return cmd
}

func newStatsPublishCommand(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish <file...>",
		Short: "Publish run record files to a stats registry",
		Long: "Publish run record files, written by --stats-file, to a stats repository as unsigned statistics. " +
			"Every file is tried; the command fails when any of them could not be read, parsed or published. " +
			"The publishing job vouches for the files with its own credentials: never publish files from untrusted (fork) jobs into a repository others read.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := flagOrEnv(cmd, "registry", "PACTO_STATS_REGISTRY")
			if repo == "" {
				return errNoStatsRegistry
			}
			var failed []string
			for _, file := range args {
				line, err := publishRecordFile(cmd, d, repo, file)
				if err != nil {
					failed = append(failed, file+": "+err.Error())
					continue
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			if len(failed) > 0 {
				return fmt.Errorf("%d of %d run records not published: %s", len(failed), len(args), strings.Join(failed, "; "))
			}
			return nil
		},
	}
	cmd.Flags().String("registry", "", "stats repository to publish to (default $PACTO_STATS_REGISTRY)")
	return cmd
}

// publishRecordFile reads, checks and publishes one record file, and returns
// its success line.
func publishRecordFile(cmd *cobra.Command, d deps, repo, file string) (string, error) {
	r, err := readRecordFile(file)
	if err != nil {
		return "", err
	}
	// Parse never recomputes the id, so a forged one would merge or split
	// distinct-change counts.
	if r.ID != runrecord.ID(r.Kind, r.Service, r.OldRevision, r.NewRevision) {
		return "", errors.New("id does not match its kind, service and revisions")
	}
	at, err := d.publishRunRecord(cmd.Context(), repo, r)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("published %s (%s %s %s) to %s (unsigned statistics)", file, r.Kind, r.Service, r.ID, at), nil
}

func readRecordFile(file string) (runrecord.Record, error) {
	f, err := os.Open(file)
	if err != nil {
		return runrecord.Record{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, runrecord.MaxRecordBytes+1))
	if err != nil {
		return runrecord.Record{}, err
	}
	return runrecord.Parse(data)
}
