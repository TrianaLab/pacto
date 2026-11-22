package backstage

import (
	"context"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// build builds a snapshot from sources.
func build(t *testing.T, sources ...fleet.Source) *fleet.FleetSnapshot {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixed }}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// workloads is a workload source in cluster (none when empty) reading the
// shop/cart Deployment.
func workloads(id, cluster string) fleet.Source {
	col := &fleet.Collection{InferredServices: []fleet.RawInferredService{{Namespace: "shop", Kind: "Deployment", Name: "cart"}}}
	if cluster != "" {
		col.Cluster = &fleet.Cluster{ID: cluster, Name: cluster}
	}
	return fleet.NewMemorySource(id, fleet.SourceKindKubernetesWorkloads, col)
}

// inferredComponents is every Component es marks inferred.
func inferredComponents(es []Entity) []Entity {
	var out []Entity
	for _, e := range ofKind(es, kindComp) {
		if e.Metadata.Annotations[AnnotationProvenance] == provenanceInferred {
			out = append(out, e)
		}
	}
	return out
}

func TestInferredServiceAnnotatesWorkload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources []fleet.Source
		want    []string
	}{
		{"no cluster", []fleet.Source{workloads("w", "")}, []string{"Deployment shop/cart"}},
		{"two clusters", []fleet.Source{workloads("wa", "a"), workloads("wb", "b")}, []string{"Deployment a/shop/cart", "Deployment b/shop/cart"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comps := inferredComponents(mapSnap(build(t, tc.sources...)))
			var got []string
			for _, c := range comps {
				ann := c.Metadata.Annotations
				if ann[AnnotationSource] != "workload" || len(c.Metadata.Tags) != 1 || c.Metadata.Tags[0] != TagInferred {
					t.Errorf("%s = %+v", c.Metadata.Name, c.Metadata)
				}
				got = append(got, ann[AnnotationWorkload])
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("workloads = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInferredServiceNeverFillsOwnerOrSystem(t *testing.T) {
	snap := build(t, workloads("w", ""), fleet.NewMemorySource("r", "local", &fleet.Collection{
		Revisions: []fleet.RawRevision{rev("", "pay", "1.0.0", team("billing"), bizDomain("payments"))},
	}))
	for _, s := range snap.Services {
		if s.Inferred() {
			// What a workload label might claim: an owner and a domain.
			s.OwnerProvenance, s.Owner, s.BusinessDomain = fleet.OwnerProvenanceInferred, contract.Owner{Team: "billing"}, "payments"
		}
	}
	comps := inferredComponents(mapSnap(snap))
	if len(comps) != 1 {
		t.Fatalf("inferred components = %d, want 1", len(comps))
	}
	c := comps[0]
	if c.Spec.Owner != unknown || c.Spec.System != "" || c.Metadata.Annotations[AnnotationOwnerProvenance] != fleet.OwnerProvenanceInferred {
		t.Errorf("inferred component = %+v", c)
	}
}

func TestDeclaredServiceHasNoSourceAnnotation(t *testing.T) {
	for _, c := range ofKind(mapCols(t, goldenSources()...), kindComp) {
		ann := c.Metadata.Annotations
		if ann[AnnotationProvenance] == provenanceDeclared && (ann[AnnotationSource] != "" || ann[AnnotationWorkload] != "") {
			t.Errorf("declared %s = %v", c.Metadata.Name, ann)
		}
	}
}

func TestResourceSourceKinds(t *testing.T) {
	for _, tc := range []struct {
		kinds []string
		want  string
	}{
		{nil, ""},
		{[]string{"pod-spec", "repository", "pod-spec"}, "pod-spec,repository"},
		{[]string{"observed", "contract"}, "contract,observed"},
	} {
		var sources []fleet.ExternalSource
		for _, k := range tc.kinds {
			sources = append(sources, fleet.ExternalSource{Kind: k})
		}
		if got := sourceKinds(sources); got != tc.want {
			t.Errorf("sourceKinds(%q) = %q, want %q", tc.kinds, got, tc.want)
		}
	}
	h := newHand()
	h.external("bare", "")
	r := ofKind(h.es(), kindRes)[0]
	if _, ok := r.Metadata.Annotations[AnnotationSource]; ok {
		t.Errorf("a Resource with no source carries one: %v", r.Metadata.Annotations)
	}
}

func TestInferredExternalOwnerUnknown(t *testing.T) {
	for _, r := range ofKind(mapCols(t, goldenSources()...), kindRes) {
		if r.Spec.Owner != unknown {
			t.Errorf("%s owner = %q", r.Metadata.Name, r.Spec.Owner)
		}
		if r.Metadata.Annotations[AnnotationProvenance] == provenanceInferred && r.Metadata.Annotations[AnnotationSource] != "pod-spec" {
			t.Errorf("inferred %s = %v", r.Metadata.Name, r.Metadata.Annotations)
		}
	}
}

const stripeRepo = "ghcr.io/acme/pacto-inferred/stripe"

// stripeFleet is checkout declaring stripe and pinning its inferred contract,
// read from the installation's pacto-inferred repository.
func stripeFleet(t *testing.T) []Entity {
	t.Helper()
	checkout := rev("", "checkout", "1.0.0", func(c *contract.Contract) {
		c.Dependencies = []contract.Dependency{{Name: "stripe", Ref: "oci://" + stripeRepo, External: true}}
	})
	inferred := fleet.RawRevision{
		Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.5",
			Service: contract.Service{Name: "stripe", Version: "2023.10.16", External: "stripe"}}, FS: fstest.MapFS{}},
		ResolvedRef: stripeRepo + ":2023.10.16", Digest: "sha256:stripe",
	}
	return mapCols(t, &fleet.Collection{Revisions: []fleet.RawRevision{checkout, inferred}})
}

func TestInferredContractAnnotated(t *testing.T) {
	r := ofKind(stripeFleet(t), kindRes)
	if len(r) != 1 || r[0].Metadata.Annotations[AnnotationInferredContract] != stripeRepo {
		t.Fatalf("resources = %+v", r)
	}
}

func TestInferredContractNeverChangesResourceProvenance(t *testing.T) {
	r := ofKind(stripeFleet(t), kindRes)[0]
	if r.Metadata.Annotations[AnnotationProvenance] != provenanceDeclared || len(r.Metadata.Tags) != 0 || r.Spec.Owner != unknown {
		t.Errorf("resource = %+v", r)
	}
}

func TestResourceSourceAnnotationUsesVisibleSources(t *testing.T) {
	front := rev("", "shopfront", "1.0.0", func(c *contract.Contract) {
		c.Dependencies = []contract.Dependency{{Name: "stripe", External: true}}
	})
	vault := rev("", "vaultkeeper", "1.0.0")
	ft, vt := target("shopfront", front.Digest, contract.Owner{}), target("vaultkeeper", vault.Digest, contract.Owner{})
	vt.Scope = "secret"
	snap := snapshot(t, &fleet.Collection{
		Revisions: []fleet.RawRevision{front, vault}, Targets: []fleet.RawTarget{ft, vt},
		Observed: []fleet.ObservedEdge{{From: "vaultkeeper", To: "api.stripe.com", Count: 1, FirstSeen: fixed, LastSeen: fixed}},
	})
	source := func(q *fleet.Query) string {
		for _, r := range ofKind(Map(q), kindRes) {
			if r.Metadata.Annotations[AnnotationExternalKey] == "stripe" {
				return r.Metadata.Annotations[AnnotationSource]
			}
		}
		t.Fatal("no stripe Resource")
		return ""
	}
	if got := source(fleet.NewQuery(snap)); got != "contract,observed" {
		t.Fatalf("unrestricted source = %q", got)
	}
	view := fleet.NewQuery(snap).Restrict(func(_, ns string) bool { return ns == "shop" || ns == "" }, nil)
	if got := source(view); got != "contract" {
		t.Errorf("restricted source = %q, want contract", got)
	}
}
