// Command storystats writes the run records of examples/story's history, the
// weeks older than the story, over the Kubernetes demo's own services and
// Stages, so delivery-demo.sh seeds the Metrics page from the very generator
// the browser demo shows instead of keeping a second copy of the data. The
// story's own runs are recorded for real by the demo, so they are never
// written here. Run records are unsigned statistics.
//
//	storystats -out DIR [-now RFC3339] [-services a,b] [-publish REPO]
//
// Each record goes to DIR/NNNN.json, one per file. -services keeps only the
// records of those services, so a run that set up fewer services seeds nothing
// its cluster cannot show. -publish also attaches every record to REPO, the
// way the promoter and pacto stats publish do; pacto stats publish refuses
// operation records, which only the promoter writes, and the history holds
// some, so the seed publishes through the library itself (registry
// credentials and PACTO_INSECURE_REGISTRIES as pacto reads them).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/registry"
)

// kindStages are the Stages delivery-demo.sh creates, per service, by
// namespace with each one's upstream (TestKindTopologyMatchesHarness reads
// the script).
var kindStages = map[string]map[string]string{
	"payments":  {story.Test: "artifact", story.Prod: "stage:test/payments", "prod-eu": "stage:test/payments"},
	"checkout":  {story.Test: "artifact", story.Prod: "stage:test/checkout"},
	"orders":    {story.Prod: "artifact"},
	"pay-agent": {story.Test: "artifact", story.Prod: "stage:test/pay-agent"},
}

// kindStatsServices is the story's topology narrowed to the kind cluster: its
// services, each with only the Stages the cluster has.
func kindStatsServices() []story.StatsService {
	var out []story.StatsService
	for _, s := range story.StatsServices() {
		stages, ok := kindStages[s.Name]
		if !ok {
			continue
		}
		s.Stages = nil
		for _, ns := range slices.Sorted(maps.Keys(stages)) {
			s.Stages = append(s.Stages, story.StatsStage{Namespace: ns, Name: s.Name, Environment: ns, Upstream: stages[ns]})
		}
		out = append(out, s)
	}
	return out
}

// history is the history records at now of the services named, or of every
// kind service when none is; a name the kind demo lacks has none.
func history(now time.Time, services []string) ([]runrecord.Record, error) {
	recs, err := story.HistoryRecords(now, kindStatsServices())
	if err != nil || len(services) == 0 {
		return recs, err
	}
	return slices.DeleteFunc(recs, func(r runrecord.Record) bool { return !slices.Contains(services, r.Service) }), nil
}

// write writes recs to dir, one file each.
func write(dir string, recs []runrecord.Record) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i, r := range recs {
		b, err := runrecord.Encode(r)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%04d.json", i)), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// publish attaches recs to the stats repository at path.
func publish(ctx context.Context, path string, recs []runrecord.Record) error {
	repo, err := registry.Open(path, oci.NewKeychain(oci.EnvCredentialOptions()), oci.EnvInsecureRegistries())
	if err != nil {
		return err
	}
	for _, r := range recs {
		if _, err := registry.Publish(ctx, repo, r); err != nil {
			return fmt.Errorf("publishing %s's %s record: %w", r.Service, r.Kind, err)
		}
	}
	return nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("storystats", flag.ContinueOnError)
	out := fs.String("out", "", "directory to write the records to (required)")
	nowFlag := fs.String("now", "", "the time the history ends before, RFC 3339 (default now)")
	services := fs.String("services", "", "comma-separated services to keep (default every kind demo service)")
	repo := fs.String("publish", "", "stats repository to publish the records to as well")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("-out is required")
	}
	now := time.Now()
	if *nowFlag != "" {
		var err error
		if now, err = time.Parse(time.RFC3339, *nowFlag); err != nil {
			return err
		}
	}
	var keep []string
	if *services != "" {
		keep = strings.Split(*services, ",")
	}
	recs, err := history(now, keep)
	if err == nil {
		err = write(*out, recs)
	}
	if err == nil && *repo != "" {
		err = publish(context.Background(), *repo, recs)
	}
	if err != nil {
		return err
	}
	fmt.Println(len(recs))
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "storystats:", err)
		os.Exit(1)
	}
}
