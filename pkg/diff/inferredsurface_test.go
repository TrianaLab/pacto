package diff

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const surfaceCatalog = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// surfaceFS is a bundle FS holding only a record with surface s.
func surfaceFS(s *contract.InferredSurface) fstest.MapFS {
	r := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"}, Surface: s}
	return fstest.MapFS{contract.InferredRecordFile: {Data: r.Marshal()}}
}

func baseSurface() *contract.InferredSurface {
	return &contract.InferredSurface{Identity: "stripe", Class: "vendor-api", AsOf: "2026-11-17T10:00:00Z",
		Completeness: &contract.SurfaceCompleteness{State: "complete"},
		Hosts:        []contract.SurfaceValue{{Value: "api.stripe.com", Sources: []string{"config"}}},
		Ports:        []contract.SurfaceValue{{Value: "443", Sources: []string{"config"}}},
		Clients:      []contract.SurfaceValue{{Value: "npm:stripe/14", Sources: []string{"repo"}}}}
}

func TestDiffServiceExternalNonBreaking(t *testing.T) {
	ctx := context.Background()
	none, a, b := bareContract(), bareContract(), bareContract()
	a.Service.External, b.Service.External = "stripe", "host/stripe.com"
	for _, tc := range []struct {
		old, new *contract.Contract
		want     ChangeType
	}{{none, a, Added}, {a, none, Removed}, {a, b, Modified}} {
		r := Compare(ctx, tc.old, tc.new, nil, nil)
		if len(r.Changes) != 1 || r.Changes[0].Path != "service.external" || r.Changes[0].Type != tc.want ||
			r.Changes[0].Classification != NonBreaking {
			t.Fatalf("%s: changes = %+v", tc.want, r.Changes)
		}
	}
}

func TestDiffSurfaceValueAddedNonBreaking(t *testing.T) {
	c := bareContract()
	more := baseSurface()
	more.Hosts = append(more.Hosts, contract.SurfaceValue{Value: "files.stripe.com", Sources: []string{"pods"}})
	more.Clients = nil
	got := inferredChanges(Compare(context.Background(), c, c, surfaceFS(baseSurface()), surfaceFS(more)))
	if len(got) != 2 {
		t.Fatalf("changes = %+v", got)
	}
	for _, ch := range got {
		if ch.Classification != NonBreaking || ch.Reason != inferredChangeReason {
			t.Fatalf("change = %+v", ch)
		}
	}
	// Hosts, ports then clients, each by value.
	if got[0].Path != "inferred.surface.hosts" || got[0].Type != Added || got[0].NewValue != "files.stripe.com" ||
		got[1].Path != "inferred.surface.clients" || got[1].Type != Removed || got[1].OldValue != "npm:stripe/14" {
		t.Fatalf("changes = %+v", got)
	}
	// A record with no surface on one side compares as an empty surface.
	if got := inferredChanges(Compare(context.Background(), c, c, nil, surfaceFS(baseSurface()))); len(got) != 3 {
		t.Fatalf("one side missing: %+v", got)
	}
}

func TestDiffSurfaceProvenanceOnlyNoChange(t *testing.T) {
	c := bareContract()
	other := baseSurface()
	other.AsOf = "2026-11-18T10:00:00Z"
	other.Completeness = &contract.SurfaceCompleteness{State: "partial", Limitations: []string{"EXTERNALS_UNREPORTED"}}
	other.Class = "unclassified"
	other.Catalog = &contract.InferredCatalogMatch{Origin: "default", Digest: surfaceCatalog, Entry: "stripe"}
	other.Hosts[0].Sources = []string{"config", "pods"}
	other.Hosts[0].Catalog = surfaceCatalog
	other.Hosts[0].FirstSeen, other.Hosts[0].LastSeen = "2026-11-01T00:00:00Z", "2026-11-18T00:00:00Z"
	if got := inferredChanges(Compare(context.Background(), c, c, surfaceFS(baseSurface()), surfaceFS(other))); len(got) != 0 {
		t.Fatalf("provenance-only diff: %+v", got)
	}
}
