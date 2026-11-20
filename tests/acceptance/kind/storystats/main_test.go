package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/registry"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// TestKindTopologyMatchesHarness: the Stages this command seeds history for
// are exactly the ones delivery-demo.sh creates, so the signed-in Metrics page
// names no Stage the cluster lacks.
func TestKindTopologyMatchesHarness(t *testing.T) {
	script, err := os.ReadFile("../delivery-demo.sh")
	if err != nil {
		t.Fatal(err)
	}
	var harness []string
	for _, m := range regexp.MustCompile(`(?m)^\s*environment ([a-z-]+) ([a-z-]+) `).FindAllStringSubmatch(string(script), -1) {
		harness = append(harness, m[1]+"/"+m[2])
	}
	var seeded []string
	for _, s := range kindStatsServices() {
		for _, st := range s.Stages {
			seeded = append(seeded, s.Name+"/"+st.Namespace)
		}
	}
	slices.Sort(harness)
	slices.Sort(seeded)
	if !slices.Equal(slices.Compact(harness), seeded) {
		t.Fatalf("delivery-demo.sh creates Stages %v, storystats seeds %v", harness, seeded)
	}
}

// storyDigests are every digest and version of the story's own revisions.
func storyDigests() map[string]bool {
	out := map[string]bool{}
	for _, s := range story.StatsServices() {
		for _, r := range s.Revisions {
			out[r.Digest], out[r.Artifact], out[r.Version] = true, true, true
		}
	}
	return out
}

// TestStoryStatsWritesHistoryOnly: one file per history record, none naming
// a revision of the story, which the demo records for real.
func TestStoryStatsWritesHistoryOnly(t *testing.T) {
	now := story.Now
	want, err := story.HistoryRecords(now, kindStatsServices())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := run([]string{"-out", dir, "-now", now.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	t.Logf("%d history records", len(files))
	if len(files) != len(want) || len(want) == 0 {
		t.Fatalf("%d files, want %d", len(files), len(want))
	}
	known := storyDigests()
	for _, f := range files {
		b, _ := os.ReadFile(f)
		r, err := runrecord.Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, v := range []string{r.OldRevision, r.NewRevision, r.BundleDigest, r.DeployDigest, r.From, r.To} {
			if known[v] || known[strings.TrimPrefix(v, "sha256:")] {
				t.Errorf("%s names the story's revision %s", f, v)
			}
		}
		if now.Sub(r.RecordedAt) <= 30*24*time.Hour {
			t.Errorf("%s is recorded %s, inside the story's 30 days", f, r.RecordedAt)
		}
	}
}

func TestStoryStatsKeepsOnlyNamedServices(t *testing.T) {
	recs, err := history(story.Now, []string{"orders", "fraud"})
	if err != nil || len(recs) == 0 {
		t.Fatalf("%d records: %v", len(recs), err)
	}
	for _, r := range recs {
		if r.Service != "orders" {
			t.Errorf("a %s record is kept", r.Service)
		}
	}
}

// TestStoryStatsPublishes: every record, operation records included, reads
// back from the repository it is published to.
func TestStoryStatsPublishes(t *testing.T) {
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	path := strings.TrimPrefix(srv.URL, "http://") + "/demo/pacto-stats"
	if err := run([]string{"-out", t.TempDir(), "-services", "orders", "-publish", path}); err != nil {
		t.Fatal(err)
	}
	want, _ := history(time.Now(), []string{"orders"})
	repo, err := registry.Open(path, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	set, err := registry.Read(t.Context(), repo, nil)
	if err != nil || len(set.Entries) != len(want) {
		t.Fatalf("read %d records back, want %d: %v", len(set.Entries), len(want), err)
	}
	if !slices.ContainsFunc(set.Entries, func(e stats.Entry) bool { return e.Record.Kind == runrecord.KindOperation }) {
		t.Error("no operation record reads back")
	}
	if run([]string{"-out", t.TempDir(), "-publish", "not a repository"}) == nil {
		t.Error("a bad repository is accepted")
	}
}

func TestStoryStatsRefusesBadFlags(t *testing.T) {
	for _, args := range [][]string{{}, {"-out", t.TempDir(), "-now", "yesterday"}, {"-nope"}} {
		if run(args) == nil {
			t.Errorf("%v: accepted", args)
		}
	}
}
