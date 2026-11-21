package fleetgen

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

var genNow = time.Date(2026, 11, 20, 9, 40, 0, 0, time.UTC)

// build is the snapshot of Collection(n, seed) read as one Kubernetes source.
func build(t *testing.T, n int, seed uint64) *fleet.FleetSnapshot {
	t.Helper()
	start := time.Now()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return genNow }},
		fleet.NewMemorySource("fleetgen", fleet.SourceKindKubernetes, Collection(n, seed, genNow)))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("n=%d snapshot built in %v", n, time.Since(start).Round(time.Millisecond))
	return snap
}

func sizes() []int {
	if os.Getenv("PACTO_SCALE_FULL") == "1" {
		return []int{40, 400, 3000, 10000}
	}
	return []int{40, 400, 3000}
}

func TestFleetgenDeterministic(t *testing.T) {
	a, b := build(t, 400, 7), build(t, 400, 7)
	if a.SnapshotID == "" || a.SnapshotID != b.SnapshotID {
		t.Errorf("same seed, snapshots %q and %q", a.SnapshotID, b.SnapshotID)
	}
	if c := build(t, 400, 8); c.SnapshotID == a.SnapshotID {
		t.Error("another seed built the same snapshot")
	}
}

func TestFleetgenSizes(t *testing.T) {
	for _, n := range sizes() {
		snap := build(t, n, 1)
		inferred := 0
		for _, s := range snap.Services {
			if s.Inferred() {
				inferred++
			}
		}
		if len(snap.Services) != n || inferred != n*12/100 {
			t.Errorf("n=%d: %d services, %d inferred", n, len(snap.Services), inferred)
		}
	}
}

func TestFleetgenProportions(t *testing.T) {
	snap := build(t, 3000, 1)
	teams := map[string]bool{}
	for _, s := range snap.Services {
		if !s.Inferred() {
			teams[s.Owner.Team] = true
		}
	}
	if len(teams) <= 40 {
		t.Errorf("%d teams at 3000, want past 40", len(teams))
	}
	if len(snap.Externals) != externals {
		t.Errorf("%d externals", len(snap.Externals))
	}
	previews := map[string]bool{}
	for _, tr := range snap.Targets {
		if strings.HasPrefix(tr.Environment, "pr-") {
			previews[tr.Environment] = true
		}
	}
	if len(previews) != 30 {
		t.Errorf("%d previews running", len(previews))
	}
	q := fleet.NewQuery(snap)
	checkAttention(t, q)
	if k := q.Overview().Summary.Knowledge.ContractCompleteness; k.FullyDescribed == 0 || k.FullyDescribed == k.Judged {
		t.Errorf("fully described %d of %d judged, want some and not all", k.FullyDescribed, k.Judged)
	}
}

// checkAttention asserts every seeded problem reaches attention, so the
// generated ends and links really resolve.
func checkAttention(t *testing.T, q *fleet.Query) {
	t.Helper()
	codes, cats := map[string]int{}, map[string]int{}
	for _, cat := range []string{"cross-environment", "health", "non-compliant", "out-of-range", "undeclared"} {
		list, err := q.Attention(fleet.AttentionFilter{Category: cat, Limit: fleet.MaxAttentionLimit})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range list.Items {
			codes[it.Code]++
			cats[cat]++
		}
	}
	for _, cat := range []string{"non-compliant", "out-of-range", "undeclared"} {
		if cats[cat] == 0 {
			t.Errorf("no %s item; codes %v", cat, codes)
		}
	}
	for _, code := range []string{"CROSS_ENVIRONMENT_CALL", "CROSS_ENVIRONMENT_PREVIEW_CALL", "HEALTH_FAILING"} {
		if codes[code] == 0 {
			t.Errorf("no %s item; items %v", code, codes)
		}
	}
}

func TestFleetgenValidContracts(t *testing.T) {
	col := Collection(400, 1, genNow)
	for _, r := range col.Revisions {
		yml, err := r.Bundle.Raw()
		if err != nil {
			t.Fatal(err)
		}
		res := validation.Validate(r.Bundle.Contract, yml, r.Bundle.FS)
		if !res.IsValid() {
			t.Fatalf("%s: %+v", r.Bundle.Contract.Service.Name, res.Errors)
		}
		if _, err := contract.Parse(strings.NewReader(string(yml))); err != nil {
			t.Fatal(err)
		}
	}
}
