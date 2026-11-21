package fleet

import (
	"reflect"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// callsCart is orders 1.1.0 declaring a dependency named like the inferred
// cart workload, which no contract provides.
func callsCart() RawRevision {
	c := usesPostgres("orders", "1.1.0")
	c.Dependencies = append(c.Dependencies, contract.Dependency{Name: "cart", Ref: "oci://ghcr.io/acme/cart", Required: true, Compatibility: "^1.0.0"})
	return ociRev(acme, c, nil)
}

func TestInferredRelationshipNeverSatisfiesDependency(t *testing.T) {
	q := goldenWith(t, true, NewMemorySource("oci2", "oci", &Collection{Revisions: []RawRevision{callsCart()}}), kernel(call("orders", cartName)))
	orders := acmeService(q, "orders").Key
	if !slices.ContainsFunc(q.snap.InferredRelationships, func(r Relationship) bool { return r.FromService == orders }) {
		t.Fatalf("no inferred edge from orders: %+v", q.snap.InferredRelationships)
	}
	found := false
	for _, rel := range q.snap.Relationships {
		if rel.FromService != orders || rel.To != "cart" {
			continue
		}
		found = true
		if rel.Resolved || !rel.missing() || rel.ToService != "" {
			t.Errorf("the inferred cart satisfied a declared dependency: %+v", rel)
		}
	}
	if !found {
		t.Fatal("no declared cart dependency")
	}
}

func TestServiceSummaryIgnoresInferredEdges(t *testing.T) {
	summary := func(other ...Source) ServiceSummary {
		q := goldenWith(t, true, other...)
		sv, err := q.GetService(string(acmeService(q, "orders").Key))
		if err != nil {
			t.Fatal(err)
		}
		sum, _ := q.serviceSummary(sv)
		return sum
	}
	sameJSON(t, "service summary", summary(), summary(kernel(call("orders", cartName), call(cartName, "orders"))))
}

func TestAttentionNoUndeclaredForInferredEdge(t *testing.T) {
	q := goldenWith(t, true, kernel(call("orders", cartName), call(cartName, ledgerName)))
	if len(q.snap.InferredRelationships) != 2 {
		t.Fatalf("inferred relationships %+v", q.snap.InferredRelationships)
	}
	if got := q.observedOnly(""); len(got) != 0 {
		t.Errorf("observed-only relationships %+v", got)
	}
	l, err := q.Attention(AttentionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range l.Items {
		if it.Code == "DEPENDENCY_UNDECLARED" {
			t.Errorf("an inferred edge raised %+v", it)
		}
	}
}

func TestRestrictCallersCountsByEntity(t *testing.T) {
	web, cart := ServiceKey("web"), NewServiceKey(cartName)
	s := &FleetSnapshot{
		Services: map[ServiceKey]*ServiceRecord{web: {Key: web, Name: "web"}, cart: {Key: cart, Name: cartName}},
		UncontractedCallers: []UncontractedCaller{
			{Caller: cartName, ToService: web, Entity: cart},
			{Caller: "cart.shop.svc", ToService: web, Entity: cart},
			{Caller: "k8s:secret/Job/x", ToService: web},
		},
	}
	deps, dependents := map[ServiceKey]map[ServiceKey]bool{}, map[ServiceKey]map[ServiceKey]bool{}
	if got := restrictCallers(s, map[ServiceKey]bool{cart: true}, map[ClusterNamespace]bool{{Namespace: "secret"}: true}, false, deps, dependents); len(got) != 0 {
		t.Errorf("kept %+v", got)
	}
	want := map[ServiceKey]map[ServiceKey]bool{web: {cart: true, "caller:k8s:secret/Job/x": true}}
	if !reflect.DeepEqual(dependents, want) {
		t.Errorf("dependents %v, want the cart entity once and the entity-less caller by name", dependents)
	}
}

func TestObservedConsumersLinksInferredEntity(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, "postgresql"), call("gift-cards", "postgresql"), call("10.0.0.1", "postgresql")))
	d, err := q.EntityDetail(KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	items := d.Offering.ObservedConsumers.Items
	var cart *ObservedConsumer
	for i := range items {
		if items[i].Caller == cartName {
			cart = &items[i]
		}
	}
	if cart == nil || !cart.Uncontracted || cart.Consumer == nil || cart.Consumer.Key != string(NewServiceKey(cartName)) {
		t.Fatalf("the cart row does not link its inferred service: %+v", items)
	}
	if got := cart.label(); got != cart.Consumer.Label+" (no contract, last seen unknown)" {
		t.Errorf("label %q", got)
	}
	serviceRowsFirst(t, items)
	// A caller whose inferred service the view does not hold keeps no link.
	delete(q.snap.Services, NewServiceKey(cartName))
	d, _ = q.EntityDetail(KindOffering, "postgresql")
	for _, it := range d.Offering.ObservedConsumers.Items {
		if it.Uncontracted && it.Consumer != nil {
			t.Errorf("dangling consumer link %+v", it)
		}
	}
}

// serviceRowsFirst fails unless items holds a service row and every service
// row sorts before every uncontracted row.
func serviceRowsFirst(t *testing.T, items []ObservedConsumer) {
	t.Helper()
	i := slices.IndexFunc(items, func(c ObservedConsumer) bool { return c.Uncontracted })
	if i <= 0 || slices.ContainsFunc(items[i:], func(c ObservedConsumer) bool { return !c.Uncontracted }) {
		t.Errorf("service rows do not all sort first: %+v", items)
	}
}
