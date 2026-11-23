package fleetsrc

import (
	"context"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// inferredBundle is stripe's inferred contract at version, its surface
// listing client.
func inferredBundle(version, client string) *contract.Bundle {
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "stripe", Version: version, External: "stripe"}}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"},
		Fields: []contract.Inferred{}, Surface: &contract.InferredSurface{Identity: "stripe", Class: "vendor-api", AsOf: "2026-07-29T12:00:00Z",
			Completeness: &contract.SurfaceCompleteness{State: "complete"}, Clients: []contract.SurfaceValue{{Value: client, Sources: []string{"k8s"}}}}}
	return &contract.Bundle{Contract: c, FS: fstest.MapFS{contract.InferredRecordFile: {Data: rec.Marshal()}}}
}

func TestOCISourceReadsTwoNewestInferredTags(t *testing.T) {
	const repo = "ghcr.io/acme/pacto-inferred/stripe"
	const svc = "ghcr.io/acme/checkout"
	store := &fakeStore{
		bundles: map[string]*contract.Bundle{
			repo + ":0.0.1": inferredBundle("0.0.1", "stripe-go/75"),
			repo + ":0.0.2": inferredBundle("0.0.2", "stripe-go/76"),
			repo + ":0.0.3": inferredBundle("0.0.3", "stripe-go/77"),
			svc + ":1.1.0":  bundleFor("checkout"),
		},
		digest: map[string]string{repo + ":0.0.2": validDigest("2"), repo + ":0.0.3": validDigest("3"), svc + ":1.1.0": validDigest("c")},
		tags:   map[string][]string{repo: {"0.0.1", "0.0.3", "0.0.2", "latest"}, svc: {"1.0.0", "1.1.0"}},
	}
	s := NewOCISource("oci", store, []string{"oci://" + repo, svc})
	s.Provenance = func(_ context.Context, source, _ string, _ *contract.Contract) *fleet.BundleProvenance {
		return &fleet.BundleProvenance{Source: source, Signature: fleet.BundleSignature{Status: fleet.SignatureUnverified, Source: fleet.SignatureSourceOperator, Bundles: 1}}
	}
	col, err := s.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for _, r := range col.Revisions {
		versions = append(versions, r.Bundle.Contract.Service.Version+" "+r.ResolvedRef)
	}
	want := []string{"0.0.3 oci://" + repo + "@" + validDigest("3"), "0.0.2 oci://" + repo + "@" + validDigest("2"), "1.0.0 oci://" + svc + "@" + validDigest("c")}
	if !slices.Equal(versions, want) {
		t.Fatalf("versions = %v (%+v), want %v", versions, col.Limitations, want)
	}
	// The real source yields the pair ExternalProvider.Change compares.
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{Now: func() time.Time { return time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC) }}, s)
	if err != nil {
		t.Fatal(err)
	}
	rec := snap.Externals["stripe"]
	if rec == nil || rec.Provider == nil || rec.Provider.Change == nil {
		t.Fatalf("stripe = %+v", rec)
	}
	if c := rec.Provider.Change; c.From != "0.0.2" || c.To != "0.0.3" || !c.Checked || len(c.Added) != 1 || c.Added[0] != "clients stripe-go/77" ||
		len(c.Removed) != 1 || c.Removed[0] != "clients stripe-go/76" {
		t.Errorf("change = %+v", c)
	}
}

func TestOCISourceInferredRepositoryWithoutTagsFallsBack(t *testing.T) {
	const repo = "ghcr.io/acme/pacto-inferred/stripe"
	col, err := NewOCISource("oci", &fakeStore{}, []string{repo}).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Revisions) != 0 || len(col.Limitations) != 1 {
		t.Errorf("revisions %+v limitations %+v, want one unresolved ref", col.Revisions, col.Limitations)
	}
}
