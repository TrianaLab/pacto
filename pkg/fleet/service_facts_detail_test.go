package fleet

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func serviceDetail(t *testing.T, q *Query, key ServiceKey) *ServiceDetailData {
	t.Helper()
	d, err := q.EntityDetail(KindService, string(key))
	if err != nil {
		t.Fatal(err)
	}
	return d.Service
}

// The service page carries the inferred owner only where the owner is
// inferred, on a declared service and on an inferred one alike.
func TestServiceOwnershipInferred(t *testing.T) {
	q := shopFleet(t, workloadsSource("k8s-workloads", nil, rawInferred("shop", "StatefulSet", "cache")),
		factCatalog(fact("ledger", "team-ledger"), fact("orders", "team-other"), fact("cache", "team-cache")))
	want := &InferredFact{Value: "team:team-ledger", Source: factSource, Where: "component:default/ledger", AsOf: &factAt}
	ledger := serviceDetail(t, q, shop(q, "ledger").Key)
	if ledger.Ownership == nil || ledger.Ownership.Provenance != OwnerProvenanceInferred || ledger.Ownership.Declared || !reflect.DeepEqual(ledger.Ownership.Inferred, want) {
		t.Errorf("ledger ownership %+v", ledger.Ownership)
	}
	if o := serviceDetail(t, q, shop(q, "orders").Key).Ownership; o.Inferred != nil || o.Provenance != OwnerProvenanceDeclared {
		t.Errorf("a declared owner carries no inferred one: %+v", o)
	}
	cache := serviceDetail(t, q, NewServiceKey(cacheName))
	if cache.Ownership == nil || cache.Ownership.Inferred == nil || cache.Ownership.Inferred.Value != "team:team-cache" {
		t.Errorf("inferred service ownership %+v", cache.Ownership)
	}
	// An inferred service no source names an owner for shows none, as before.
	plain := shopFleet(t, workloadsSource("k8s-workloads", nil, rawInferred("shop", "StatefulSet", "cache")))
	if o := serviceDetail(t, plain, NewServiceKey(cacheName)).Ownership; o != nil {
		t.Errorf("inferred service with no fact: ownership %+v", o)
	}
}

// The inferred business domain shows only where none is declared.
func TestServiceDetailInferredBusinessDomain(t *testing.T) {
	ledger, orders, cache := fact("ledger", ""), fact("orders", ""), fact("cache", "")
	ledger.BusinessDomain, orders.BusinessDomain, cache.BusinessDomain = "billing", "billing", "storage"
	q := shopFleet(t, workloadsSource("k8s-workloads", nil, rawInferred("shop", "StatefulSet", "cache")), factCatalog(ledger, orders, cache))
	if d := serviceDetail(t, q, shop(q, "ledger").Key).InferredBusinessDomain; d == nil || d.Value != "billing" || d.Source != factSource {
		t.Errorf("ledger inferred domain %+v", d)
	}
	if d := serviceDetail(t, q, shop(q, "orders").Key).InferredBusinessDomain; d != nil {
		t.Errorf("a declared domain carries no inferred one: %+v", d)
	}
	if d := serviceDetail(t, q, NewServiceKey(cacheName)).InferredBusinessDomain; d == nil || d.Value != "storage" {
		t.Errorf("inferred service domain %+v", d)
	}
}

// The new keys are additive: the released ones keep their names and values.
func TestServiceRecordJSONAddsInferredFields(t *testing.T) {
	ledger := fact("ledger", "team-ledger")
	ledger.BusinessDomain = "billing"
	base, q := shopFleet(t), shopFleet(t, factCatalog(ledger))
	var before, after map[string]json.RawMessage
	for _, c := range []struct {
		s   *ServiceRecord
		out *map[string]json.RawMessage
	}{{shop(base, "ledger"), &before}, {shop(q, "ledger"), &after}} {
		data, _ := json.Marshal(c.s)
		if err := json.Unmarshal(data, c.out); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{"inferredOwner", "inferredBusinessDomain"} {
		if _, ok := after[k]; !ok {
			t.Errorf("%s missing: %v", k, after)
		}
		if _, ok := before[k]; ok {
			t.Errorf("%s present without a fact", k)
		}
	}
	for k, v := range before {
		if k == "ownerProvenance" {
			continue
		}
		if string(after[k]) != string(v) {
			t.Errorf("%s changed: %s -> %s", k, v, after[k])
		}
	}
	if string(after["ownerProvenance"]) != `"inferred"` || !strings.Contains(string(after["inferredOwner"]), `"value":"team:team-ledger"`) {
		t.Errorf("owner provenance %s inferred %s", after["ownerProvenance"], after["inferredOwner"])
	}
}

func TestHasServiceFact(t *testing.T) {
	fact := Relationship{InferredSources: []InferredSource{{Kind: "call"}, {Kind: serviceFactKind}}}
	if !HasServiceFact(fact) || HasServiceFact(Relationship{InferredSources: []InferredSource{{Kind: "call"}}}) || HasServiceFact(Relationship{}) {
		t.Error("HasServiceFact")
	}
}
