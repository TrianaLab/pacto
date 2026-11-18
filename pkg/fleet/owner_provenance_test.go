package fleet

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// ownerProvenanceFleet builds the service "pay" from an optional revision owner
// and targets, each with the owner its observing source read.
func ownerProvenanceFleet(t *testing.T, revOwner contract.Owner, targets ...RawTarget) *Query {
	t.Helper()
	c := &Collection{Targets: targets}
	if !revOwner.IsEmpty() {
		c.Revisions = []RawRevision{{Digest: "sha256:pay", Bundle: &contract.Bundle{Contract: &contract.Contract{
			PactoVersion: "2.0", Service: contract.Service{Name: "pay", Version: "1.0.0", Owner: revOwner},
		}}}}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", SourceKindKubernetes, c))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func payTarget(scope string, owner contract.Owner) RawTarget {
	return RawTarget{Scope: scope, Kind: SourceKindKubernetes, Name: "pay-" + scope, Service: "pay", Owner: owner, Compliance: StatusCompliant}
}

func payRecord(t *testing.T, q *Query) *ServiceRecord {
	t.Helper()
	s := q.SnapshotForSerialization().Services[NewServiceKey("pay")]
	if s == nil {
		t.Fatal("service pay is absent")
	}
	return s
}

func TestOwnerProvenanceDeclared(t *testing.T) {
	s := payRecord(t, ownerProvenanceFleet(t, contract.Owner{Team: "pay"}))
	if s.Owner.Team != "pay" || s.OwnerProvenance != OwnerProvenanceDeclared {
		t.Errorf("owner = %+v provenance = %q, want team pay declared", s.Owner, s.OwnerProvenance)
	}
}

func TestOwnerProvenanceViaTarget(t *testing.T) {
	s := payRecord(t, ownerProvenanceFleet(t, contract.Owner{}, payTarget("a", contract.Owner{Team: "pay"})))
	if s.Owner.Team != "pay" || s.OwnerProvenance != OwnerProvenanceDeclaredViaTarget {
		t.Errorf("owner = %+v provenance = %q, want team pay declared-via-target", s.Owner, s.OwnerProvenance)
	}
}

func TestOwnerProvenanceEmptyWhenUnowned(t *testing.T) {
	s := payRecord(t, ownerProvenanceFleet(t, contract.Owner{}, payTarget("a", contract.Owner{})))
	if !s.Owner.IsEmpty() || s.OwnerProvenance != "" {
		t.Errorf("owner = %+v provenance = %q, want both empty", s.Owner, s.OwnerProvenance)
	}
}

func TestOwnerProvenanceRevisionWinsOverTarget(t *testing.T) {
	s := payRecord(t, ownerProvenanceFleet(t, contract.Owner{Team: "pay"}, payTarget("a", contract.Owner{Team: "ops"})))
	if s.Owner.Team != "pay" || s.OwnerProvenance != OwnerProvenanceDeclared {
		t.Errorf("owner = %+v provenance = %q, want the revision's team pay, declared", s.Owner, s.OwnerProvenance)
	}
}

// The only owner comes from a target the caller may not see: the restricted
// record has neither the owner nor its provenance.
func TestRestrictOwnerProvenanceFromHiddenTarget(t *testing.T) {
	q := ownerProvenanceFleet(t, contract.Owner{}, payTarget("secret", contract.Owner{Team: "pay"}), payTarget("shop", contract.Owner{}))
	if s := payRecord(t, q); s.OwnerProvenance != OwnerProvenanceDeclaredViaTarget {
		t.Fatalf("unrestricted provenance = %q, want declared-via-target", s.OwnerProvenance)
	}
	s := payRecord(t, q.Restrict(func(ns string) bool { return ns == "shop" || ns == "" }, nil))
	if !s.Owner.IsEmpty() || s.OwnerProvenance != "" {
		t.Errorf("restricted owner = %+v provenance = %q, want both empty", s.Owner, s.OwnerProvenance)
	}
}

func TestOwnershipInfoProvenance(t *testing.T) {
	q := ownerProvenanceFleet(t, contract.Owner{Team: "pay"})
	d, err := q.EntityDetail(KindService, string(NewServiceKey("pay")))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Service.Ownership.Provenance; got != payRecord(t, q).OwnerProvenance || got != OwnerProvenanceDeclared {
		t.Errorf("service ownership provenance = %q, want the record's (declared)", got)
	}
	rk := payRecord(t, q).Revisions[0]
	rd, err := q.EntityDetail(KindRevision, string(rk))
	if err != nil {
		t.Fatal(err)
	}
	if got := rd.Revision.Ownership.Provenance; got != "" {
		t.Errorf("revision ownership provenance = %q, want none", got)
	}
}

func TestOwnerProvenanceEnumMatchesConstants(t *testing.T) {
	want := []string{OwnerProvenanceDeclared, OwnerProvenanceDeclaredViaTarget, OwnerProvenanceAttested, OwnerProvenanceInferred}
	slices.Sort(want)
	for _, f := range []struct {
		typ   reflect.Type
		field string
	}{{reflect.TypeFor[ServiceRecord](), "OwnerProvenance"}, {reflect.TypeFor[OwnershipInfo](), "Provenance"}} {
		sf, ok := f.typ.FieldByName(f.field)
		if !ok {
			t.Fatalf("%s.%s is missing", f.typ.Name(), f.field)
		}
		got := strings.Split(sf.Tag.Get("enum"), ",")
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s.%s enum = %v, want %v", f.typ.Name(), f.field, got, want)
		}
	}
}

func TestOwnerProvenanceJSON(t *testing.T) {
	for _, c := range []struct {
		name    string
		q       *Query
		present bool
	}{
		{"declared", ownerProvenanceFleet(t, contract.Owner{Team: "pay"}), true},
		{"unowned", ownerProvenanceFleet(t, contract.Owner{}, payTarget("a", contract.Owner{})), false},
	} {
		v, err := c.q.GetService("pay")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(raw), `"ownerProvenance"`); got != c.present {
			t.Errorf("%s: ownerProvenance present = %v, want %v in %s", c.name, got, c.present, raw)
		}
	}
}
