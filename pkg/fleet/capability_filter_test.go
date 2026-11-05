package fleet

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestEntities_CapabilityFilter(t *testing.T) {
	q := productFleet(t)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Capability: "health"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entities) != 1 || l.Entities[0].Label != "beta" {
		t.Fatalf("services declaring health = %+v, want only beta", l.Entities)
	}
	if got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Capability: "metrics"}); len(got) != 0 {
		t.Errorf("services declaring metrics = %v", got)
	}
	var iq *InvalidQueryError
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindTarget}, Capability: "health"}); !errors.As(err, &iq) {
		t.Errorf("a capability filter on targets = %v, want InvalidQueryError", err)
	}
	if got := entityKeys(t, q, EntityFilter{Capability: "health"}); len(got) != 1 {
		t.Errorf("an every-kind capability query = %v, want only the service", got)
	}
	all, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}})
	if err != nil {
		t.Fatal(err)
	}
	if c := all.Aggregate.Capabilities; len(c) != 1 || c[0] != (TypeCount{Type: "health", Services: 1}) || all.Aggregate.CapabilityTypes != 1 {
		t.Errorf("capability facet = %+v (%d types)", c, all.Aggregate.CapabilityTypes)
	}
}

func TestAggregate_CapabilityFacetOrderedAndBounded(t *testing.T) {
	var revs []RawRevision
	for i := range maxCapabilityFacet + 2 {
		caps := []contract.Capability{{Type: fmt.Sprintf("x-cap-%03d", i)}}
		if i < 2 {
			caps = append(caps, contract.Capability{Type: "metrics"})
		}
		k := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: fmt.Sprintf("s%03d", i), Version: "1.0.0"}, Capabilities: caps}
		revs = append(revs, RawRevision{Bundle: &contract.Bundle{Contract: k, FS: fstest.MapFS{}}, Digest: fmt.Sprintf("sha256:%d", i)})
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("l", "local", &Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewQuery(snap).Entities(EntityFilter{Kinds: []EntityKind{KindService}})
	if err != nil {
		t.Fatal(err)
	}
	c := l.Aggregate.Capabilities
	if len(c) != maxCapabilityFacet || l.Aggregate.CapabilityTypes != maxCapabilityFacet+3 {
		t.Fatalf("facet = %d rows of %d types", len(c), l.Aggregate.CapabilityTypes)
	}
	if c[0] != (TypeCount{Type: "metrics", Services: 2}) || c[1].Type != "x-cap-000" {
		t.Errorf("facet starts %+v, want metrics first, then by type", c[:2])
	}
}

// A revision whose contract was not read declares nothing.
func TestServiceCapabilities_SkipsUnreadRevisions(t *testing.T) {
	q := productFleet(t)
	s := q.snap.Services[NewServiceKeyDomain("", "beta")]
	for _, rk := range s.Revisions {
		q.snap.Revisions[rk].Contract = nil
	}
	if got := q.serviceCapabilities(s); len(got) != 0 {
		t.Errorf("capabilities = %v", got)
	}
}

// Two extensions are two capabilities, keyed by their reference.
func TestServiceCapabilities_UsesAssertionKey(t *testing.T) {
	k := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "x", Version: "1.0.0"}, Capabilities: []contract.Capability{
		{Type: contract.CapabilityExtension, Ref: "acme.io/sla"}, {Type: contract.CapabilityExtension, Ref: "acme.io/cost"}, {Type: "health"},
	}}
	q := buildDomainSnap(t, "local", &Collection{Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: k, FS: fstest.MapFS{}}}}})
	want := []string{"acme.io/cost", "acme.io/sla", "health"}
	if got := q.serviceCapabilities(q.snap.Services[NewServiceKeyDomain("", "x")]); !slices.Equal(got, want) {
		t.Errorf("capabilities = %v, want %v", got, want)
	}
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}})
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, c := range l.Aggregate.Capabilities {
		types = append(types, c.Type)
	}
	if !slices.Equal(types, want) {
		t.Errorf("facet types = %v, want %v", types, want)
	}
	if got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Capability: "acme.io/cost"}); len(got) != 1 {
		t.Errorf("acme.io/cost services = %v", got)
	}
	if got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Capability: contract.CapabilityExtension}); len(got) != 0 {
		t.Errorf("extension services = %v, want none", got)
	}
}
