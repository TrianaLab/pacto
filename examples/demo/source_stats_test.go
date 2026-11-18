package main

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// The port serves the generator's records, read as a registry read would
// list them, plus the coverage trend when a newest point is given.
func TestDemoStatsServesTheStory(t *testing.T) {
	want, err := story.RunRecords(story.Now, story.StatsServices())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		coverage *runrecord.Coverage
		extra    int
	}{
		{"without coverage", nil, 0},
		{"with coverage", &runrecord.Coverage{Services: 8, SnapshotAsOf: story.Now, Completeness: "partial"}, len(story.CoverageRecords(story.Now, runrecord.Coverage{}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port, err := demoStats(story.Now, tc.coverage)
			if err != nil {
				t.Fatal(err)
			}
			set, err := port(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			checkStatsSet(t, set, want, tc.extra)
		})
	}
}

func checkStatsSet(t *testing.T, set stats.RecordSet, want []runrecord.Record, extra int) {
	t.Helper()
	if len(set.Entries) != len(want)+extra {
		t.Fatalf("entries = %d, want %d", len(set.Entries), len(want)+extra)
	}
	for i, r := range want {
		e := set.Entries[i]
		if !reflect.DeepEqual(e.Record, r) {
			t.Fatalf("entry %d = %+v, want %+v", i, e.Record, r)
		}
		if e.Anchor != runrecord.AnchorTag(r.Service) || e.Digest == "" {
			t.Fatalf("entry %d anchor %q digest %q", i, e.Anchor, e.Digest)
		}
	}
	for _, e := range set.Entries[len(want):] {
		if e.Record.Kind != runrecord.KindCoverage {
			t.Fatalf("extra entry kind %q", e.Record.Kind)
		}
	}
	if set.Truncated != "" || set.RepositoryMissing || len(set.Skipped) != 0 {
		t.Fatalf("set reads as incomplete: %+v", set)
	}
}

// A read naming services lists only their anchors, as a registry read does.
func TestDemoStatsReadsNamedServices(t *testing.T) {
	port, err := demoStats(story.Now, &runrecord.Coverage{Services: 8, SnapshotAsOf: story.Now, Completeness: "partial"})
	if err != nil {
		t.Fatal(err)
	}
	all, err := port(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := all.Entries[0].Record.Service
	named, err := port(context.Background(), []string{svc})
	if err != nil {
		t.Fatal(err)
	}
	if len(named.Entries) == 0 || len(named.Entries) == len(all.Entries) {
		t.Fatalf("a read naming %q lists %d of %d entries", svc, len(named.Entries), len(all.Entries))
	}
	for _, e := range named.Entries {
		if e.Anchor != runrecord.AnchorTag(svc) {
			t.Fatalf("a read naming %q lists anchor %q", svc, e.Anchor)
		}
	}
}

// Decision 2: the Stages the records name are the Stages the delivery views
// draw, with the same upstream kind, so the SPEC 21 paths in Metrics match.
func TestStatsTopologyMatchesDemoStages(t *testing.T) {
	d, err := newDemoDelivery(func() time.Time { return story.Now }, func(func()) {})
	if err != nil {
		t.Fatal(err)
	}
	stages, err := d.Stages(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for _, s := range stages {
		got = append(got, s.Metadata.Namespace+"/"+s.Metadata.Name+" "+upstreamKind(s.Spec.Candidates))
	}
	for _, svc := range story.StatsServices() {
		for _, s := range svc.Stages {
			want = append(want, s.Namespace+"/"+s.Name+" "+s.Upstream)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("demo Stages and story.StatsServices differ:\n got %v\nwant %v", got, want)
	}
}

func upstreamKind(c delivery.Candidates) string {
	switch {
	case c.UpstreamStage != nil:
		return "stage:" + c.UpstreamStage.Namespace + "/" + c.UpstreamStage.Name
	case c.UpstreamRecords != nil:
		return "records"
	case c.OCIRepositoryRef != nil || c.Registry != nil:
		return "artifact"
	}
	return "none"
}
