package main

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoLifecycleTileEqualsList holds the demo's "Services using a deprecated
// provider" Overview tile to the lifecycle attention list it opens: postgresql
// 16.1.0 is deprecated, so its consumers appear, and the tile counts exactly
// the distinct services the list names.
func TestDemoLifecycleTileEqualsList(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleetsrc.NewLocalSource("local", "bundles"))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	l, err := q.Attention(fleet.AttentionFilter{Category: "lifecycle", Limit: fleet.MaxAttentionLimit})
	if err != nil {
		t.Fatal(err)
	}
	services := map[string]bool{}
	for _, it := range l.Items {
		services[it.Service] = true
	}
	if len(services) == 0 {
		t.Fatal("no service uses the deprecated postgresql 16.1.0")
	}
	ov := q.Overview()
	for _, ep := range ov.EntryPoints {
		if ep.Category == "lifecycle" {
			if ep.Count != len(services) || ov.Summary.LifecycleConsumers != len(services) {
				t.Errorf("tile %d, summary %d, list %d services", ep.Count, ov.Summary.LifecycleConsumers, len(services))
			}
			return
		}
	}
	t.Error("the Overview has no lifecycle tile")
}
