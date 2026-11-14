package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// errNoStatsRegistry is what every stats command says when it has nowhere to go.
var errNoStatsRegistry = errors.New("no stats registry: pass --registry or set PACTO_STATS_REGISTRY")

func newStatsCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Total what Pacto did from its unsigned run statistics",
		Long: "Total the run records in a stats repository for a time window, as text or JSON. " +
			"Run records are unsigned statistics about Pacto runs: nothing that decides ever reads them, and this command only reads. " +
			"Every answer says how many records were read, how many could not be read and which bound cut the read; " +
			"a rate with nothing to take it over is unknown, never 0. " +
			"With --by owner or --by domain, owner and domain are inferred from the fleet snapshot the fleet source flags build.",
		Example: `  pacto stats --registry ghcr.io/acme/pacto-stats
  pacto stats --since 7d --by environment --output-format json
  pacto stats --by owner --kind impact --kind publish`,
		Args: cobra.NoArgs,
		RunE: runStats(svc, v),
	}
	cmd.PersistentFlags().String("registry", "", "stats repository to read or publish to, for example ghcr.io/acme/pacto-stats (default $PACTO_STATS_REGISTRY)")
	f := cmd.Flags()
	f.String("since", "", "start of the window: 30d, 12h, a date or RFC 3339 (default 30d)")
	f.String("until", "", "end of the window: 30d, 12h, a date or RFC 3339 (default now)")
	f.String("by", "", "group the numbers by "+strings.Join(stats.GroupBy, ", "))
	f.String("service", "", "total only the run records of this service")
	f.StringArray("kind", nil, "total only run records of this kind: "+strings.Join(statsKinds, ", ")+" (repeatable)")
	addFleetSourceFlags(f)
	_ = cmd.RegisterFlagCompletionFunc("by", staticCompletions(stats.GroupBy...))
	_ = cmd.RegisterFlagCompletionFunc("kind", staticCompletions(statsKinds...))
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
