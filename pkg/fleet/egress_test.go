package fleet

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// The kernel and gateway egress a Pacto status reports reaches the fleet as
// an observed edge from the service to a host (delivery.ObservedEdges); these
// tests pin that the existing fold makes it an observed external.

// egressFleet builds a Kubernetes fleet: each service runs in its namespace,
// with the egress edges, target limitations and provider rows given.
func egressFleet(t *testing.T, svcNS map[string]string, observed []ObservedEdge, lims map[string][]Limitation, rows ...Provider) *Query {
	t.Helper()
	col := &Collection{Observed: observed, Providers: rows}
	for name, ns := range svcNS {
		digest := "sha256:" + name
		col.Revisions = append(col.Revisions, RawRevision{Bundle: &contract.Bundle{Contract: extContract(name, "1.0.0"), FS: fstest.MapFS{}}, Digest: digest})
		col.Targets = append(col.Targets, RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: name, Service: name, Digest: digest,
			Compliance: StatusCompliant, Limitations: lims[name]})
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestKernelEgressEdgeIsObservedExternal(t *testing.T) {
	q := egressFleet(t, map[string]string{"checkout": "shop"}, []ObservedEdge{{From: "checkout", To: "api.stripe.com", Count: 1}}, nil)
	rels := externalRels(q.Snapshot())
	if len(rels) != 1 || rels[0].FromService != "checkout" || rels[0].ToExternal != "stripe" || rels[0].Provenance != ProvenanceObserved {
		t.Fatalf("edges = %+v", rels)
	}
	rec := q.Snapshot().Externals["stripe"]
	if rec == nil || !slices.ContainsFunc(rec.Sources, func(s ExternalSource) bool {
		return s.Kind == ExternalSourceObserved && s.Service == "checkout" && s.Provenance == ProvenanceObserved
	}) {
		t.Fatalf("record = %+v", rec)
	}
}

func TestEgressUnmatchedHostGroupsByRegistrableDomain(t *testing.T) {
	q := egressFleet(t, map[string]string{"web": "shop"}, []ObservedEdge{
		{From: "web", To: "erp.corp-legacy.com", Count: 1}, {From: "web", To: "db.corp-legacy.com", Count: 1}}, nil)
	rels := externalRels(q.Snapshot())
	if len(rels) != 1 || rels[0].ToExternal != "host/corp-legacy.com" {
		t.Fatalf("edges = %+v", rels)
	}
	if rec := q.Snapshot().Externals["host/corp-legacy.com"]; rec == nil || rec.Class != externalcatalog.ClassUnclassified {
		t.Errorf("record = %+v", rec)
	}
}

func TestEgressBeyondCapFoldsIntoOtherEgress(t *testing.T) {
	var observed []ObservedEdge
	for i := range MaxExternalNodes + 5 {
		observed = append(observed, ObservedEdge{From: "web", To: fmt.Sprintf("api.vendor%03d.com", i), Count: 1})
	}
	q := egressFleet(t, map[string]string{"web": "shop"}, observed, nil)
	l := listExternals(t, q, EntityFilter{})
	if l.Total != MaxExternalNodes || !hasLimitation(l.Meta.Limitations, LimitationExternalCap) {
		t.Fatalf("total %d limitations %+v", l.Total, l.Meta.Limitations)
	}
	other, err := q.GetExternal(string(OtherEgress))
	if err != nil || other.External.Folded != 6 { // 199 kept beside other egress itself
		t.Fatalf("other egress = %+v, %v", other, err)
	}
}

func TestRestrictHidesEgressEdgeOfHiddenCaller(t *testing.T) {
	q := egressFleet(t, map[string]string{"web": "shop", "vault": "secret"}, []ObservedEdge{
		{From: "web", To: "erp.corp-legacy.com", Count: 1}, {From: "vault", To: "api.stripe.com", Count: 1}}, nil)
	r := q.Restrict(anyCluster(shopView), anyClusterKind(nil))
	if _, err := r.GetExternal("stripe"); err == nil {
		t.Fatal("an external only a hidden caller reaches is found")
	}
	if rels := externalRels(r.Snapshot()); len(rels) != 1 || rels[0].FromService != "web" {
		t.Fatalf("restricted edges = %+v", rels)
	}
}

func TestEgressLimitationMakesExternalAnswerPartial(t *testing.T) {
	observed := []ObservedEdge{{From: "web", To: "api.stripe.com", Count: 1}}
	lims := map[string][]Limitation{"web": {{Code: LimitationExternalsUnreported, Source: "k8s",
		Message: "the external calls web makes cannot be ruled out: a node runs pods but has no node agent (coverage: partial, node-without-agent)"}}}
	v, err := egressFleet(t, map[string]string{"web": "shop"}, observed, lims).GetExternal("stripe")
	if err != nil || v.Meta.Completeness != CompletenessPartial {
		t.Fatalf("external = %+v, %v", v.Meta, err)
	}
	if v, _ := egressFleet(t, map[string]string{"web": "shop"}, observed, nil).GetExternal("stripe"); v.Meta.Completeness == CompletenessPartial {
		t.Fatalf("a full egress coverage is partial: %+v", v.Meta)
	}
}

// kernelEgressRow is the row delivery.KernelEgressRow builds for services.
func kernelEgressRow(services ...ServiceKey) Provider {
	return Provider{ID: ProviderKernelEgress, Name: "Kernel egress", Evidence: "node agent on 3/4 nodes", UsedFor: []string{UseExternalEgress},
		Status: SourcePartial, Scope: ScopeCluster, Services: services,
		Limitations: []Limitation{{Code: LimitationExternalsUnreported, Source: "k8s", Message: "the node agent cannot rule out an external call"}}}
}

func TestKernelEgressRowScopeCluster(t *testing.T) {
	q := egressFleet(t, map[string]string{"web": "shop", "vault": "secret"}, nil, nil, kernelEgressRow("web", "vault"))
	r := q.Restrict(anyCluster(func(ns string) bool { return ns == "shop" }), anyClusterKind(nil))
	if r == q {
		t.Fatal("a cluster-wide row must restrict a reader without cluster scope")
	}
	row := providerByID(t, r.SnapshotForSerialization().Providers, ProviderKernelEgress)
	if row.Evidence != "Kernel egress detected cluster-wide; you may read only some namespaces" || len(row.Limitations) != 1 {
		t.Fatalf("restricted row = %+v", row)
	}
	// A reader of the cluster scope keeps the node counts.
	all := q.Restrict(anyCluster(shopView), anyClusterKind(nil)).SnapshotForSerialization()
	if row := providerByID(t, all.Providers, ProviderKernelEgress); row.Evidence != "node agent on 3/4 nodes" {
		t.Fatalf("cluster reader row = %+v", row)
	}
}

func TestKernelEgressRowHiddenWhenAllServicesHidden(t *testing.T) {
	q := egressFleet(t, map[string]string{"web": "shop", "vault": "secret"}, nil, nil, kernelEgressRow("vault"))
	r := q.Restrict(anyCluster(shopView), anyClusterKind(nil)).SnapshotForSerialization()
	if slices.ContainsFunc(r.Providers, func(p Provider) bool { return p.ID == ProviderKernelEgress }) {
		t.Fatalf("a reader who sees no contributing service sees the row: %+v", r.Providers)
	}
}

func TestRedactDetectedInferredRowsUnchanged(t *testing.T) {
	shopOnly := func(ns string) bool { return ns == "shop" }
	for _, c := range []struct {
		name     string
		row      Provider
		had      int
		visible  func(string) bool
		evidence string
		narrowed bool
	}{
		{"inferred cluster-wide", Provider{Name: "Nodes", Evidence: "e", Hint: "h", Provenance: ProvenanceInferred, Scope: ScopeCluster}, 0, shopOnly,
			"Nodes detected cluster-wide; you may read only some namespaces", true},
		{"inferred lost an object", Provider{Name: "Exposure", Evidence: "e", Hint: "h", Provenance: ProvenanceInferred, ObjectsTotal: 2}, 2, shopOnly,
			"Exposure found in a namespace you cannot read", true},
		{"inferred cluster reader", Provider{Name: "Nodes", Evidence: "e", Provenance: ProvenanceInferred, Scope: ScopeCluster}, 0, shopView, "e", false},
		{"source row lost an object", Provider{Name: "Prometheus", Evidence: "e", ObjectsTotal: 2}, 2, shopOnly, "e", false},
		{"declared row", Provider{Name: "Installation", Evidence: "e", Provenance: ProvenanceDeclared}, 0, shopOnly, "e", false},
	} {
		p := c.row
		if got := detectedNarrowed(p, c.visible); got != c.narrowed {
			t.Errorf("%s: narrowed = %v", c.name, got)
		}
		redactDetected(&p, c.had, c.visible)
		if p.Evidence != c.evidence || (c.evidence != "e" && p.Hint != "") {
			t.Errorf("%s: row = %+v", c.name, p)
		}
	}
}
