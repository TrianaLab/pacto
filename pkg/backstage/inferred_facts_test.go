package backstage

import (
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var factTime = fixed.Add(-1)

// catalogFact is name owned by team (none when empty), in domain, depending
// on deps, as a catalog outside every contract reports it.
func catalogFact(name, team, domain string, deps ...string) fleet.RawServiceFact {
	f := fleet.RawServiceFact{Name: name, Entity: "component:default/" + name, BusinessDomain: domain, ObservedAt: &factTime}
	if team != "" {
		f.Owner = contract.Owner{Team: team}
	}
	for _, d := range deps {
		f.DependsOn = append(f.DependsOn, fleet.RawFactRef{Name: d})
	}
	return f
}

// factFleet is ledger (no owner, no domain) and orders (team-orders,
// commerce) in namespace shop and vault in secret, read by source sa, with a
// catalog source sb reporting facts.
func factFleet(t *testing.T, facts ...fleet.RawServiceFact) *fleet.Query {
	t.Helper()
	ledger, orders, vault := rev("", "ledger", "1.0.0"), rev("", "orders", "1.0.0", team("team-orders"), bizDomain("commerce")), rev("", "vault", "1.0.0")
	vt := target("vault", vault.Digest, contract.Owner{})
	vt.Scope = "secret"
	return fleet.NewQuery(snapshot(t,
		&fleet.Collection{Revisions: []fleet.RawRevision{ledger, orders, vault},
			Targets: []fleet.RawTarget{target("ledger", ledger.Digest, contract.Owner{}), target("orders", orders.Digest, contract.Owner{}), vt}},
		&fleet.Collection{Advisory: true, ServiceFacts: facts}))
}

func shopOnly(q *fleet.Query) *fleet.Query {
	return q.Restrict(func(_, ns string) bool { return ns == "shop" || ns == "" }, nil)
}

func TestMapperInferredOwnerAnnotation(t *testing.T) {
	q := factFleet(t, catalogFact("ledger", "team-ledger", ""), catalogFact("orders", "team-other", ""), catalogFact("vault", "team-sec", ""))
	es := Map(shopOnly(q))
	ledger := byKey(t, es, "ledger")
	ann := ledger.Metadata.Annotations
	if ledger.Spec.Owner != unknown || ann[AnnotationOwnerProvenance] != fleet.OwnerProvenanceInferred ||
		ann[AnnotationInferredOwner] != "group:default/team-ledger" || ann[AnnotationInferredOwnerSource] != "sb component:default/ledger" {
		t.Errorf("ledger owner %q annotations %v", ledger.Spec.Owner, ann)
	}
	orders := byKey(t, es, "orders")
	if orders.Spec.Owner != "group:default/team-orders" || orders.Metadata.Annotations[AnnotationInferredOwner] != "" || orders.Metadata.Annotations[AnnotationInferredOwnerSource] != "" {
		t.Errorf("a declared owner writes no inferred annotation: %q %v", orders.Spec.Owner, orders.Metadata.Annotations)
	}
	if out := encode(t, es); strings.Contains(out, "team-sec") || strings.Contains(out, "vault") {
		t.Errorf("the restricted export names a hidden service's fact:\n%s", out)
	}
	// A DRI reads as a user ref; a value that is no valid Backstage name or no
	// owner key writes nothing.
	dri := catalogFact("ledger", "", "")
	dri.Owner = contract.Owner{DRI: "alice"}
	if a := byKey(t, Map(factFleet(t, dri)), "ledger").Metadata.Annotations; a[AnnotationInferredOwner] != "user:default/alice" {
		t.Errorf("dri annotations %v", a)
	}
	bad := catalogFact("ledger", "Team_Ledger!", "")
	if a := byKey(t, Map(factFleet(t, bad)), "ledger").Metadata.Annotations; a[AnnotationInferredOwner] != "" || a[AnnotationInferredOwnerSource] != "" {
		t.Errorf("invalid name annotations %v", a)
	}
	s := &fleet.ServiceRecord{OwnerProvenance: fleet.OwnerProvenanceInferred, InferredOwner: &fleet.InferredFact{Value: "odd", Source: "sb"}}
	got := map[string]string{}
	inferredOwner(s, got)
	if len(got) != 0 {
		t.Errorf("no owner key: %v", got)
	}
}

func TestMapperInferredDomainAnnotation(t *testing.T) {
	es := Map(factFleet(t, catalogFact("ledger", "", "billing"), catalogFact("orders", "", "payments")))
	ledger := byKey(t, es, "ledger")
	if ledger.Spec.System != "" || ledger.Metadata.Annotations[AnnotationInferredBusinessDomain] != "billing" ||
		ledger.Metadata.Annotations[AnnotationInferredBusinessDomainSource] != "sb component:default/ledger" {
		t.Errorf("ledger system %q annotations %v", ledger.Spec.System, ledger.Metadata.Annotations)
	}
	orders := byKey(t, es, "orders")
	if orders.Spec.System == "" || orders.Metadata.Annotations[AnnotationInferredBusinessDomain] != "" {
		t.Errorf("a declared domain writes no inferred one: %q %v", orders.Spec.System, orders.Metadata.Annotations)
	}
	for _, e := range ofKind(es, kindSystem) {
		if e.Metadata.Name == "billing" {
			t.Errorf("an inferred domain never makes a System: %+v", e)
		}
	}
	// An unknown domain says so and still carries the inferred one.
	ann := map[string]string{}
	got := system(&fleet.ServiceRecord{BusinessDomainUnknown: true, InferredBusinessDomain: &fleet.InferredFact{Value: "billing", Source: "sb"}}, ann, nil)
	if got != "" || ann[AnnotationBusinessDomain] != unknown || ann[AnnotationInferredBusinessDomain] != "billing" || ann[AnnotationInferredBusinessDomainSource] != "sb" {
		t.Errorf("unknown domain %q %v", got, ann)
	}
}

func TestMapperInferredEdgeAnnotation(t *testing.T) {
	q := factFleet(t, catalogFact("ledger", "", "", "orders", "vault"))
	ledger := byKey(t, Map(q), "ledger")
	if len(ledger.Spec.DependsOn) != 0 || ledger.Metadata.Annotations[AnnotationInferredDependsOn] != comp("orders")+","+comp("vault") {
		t.Errorf("ledger dependsOn %v annotations %v", ledger.Spec.DependsOn, ledger.Metadata.Annotations)
	}
	if got := byKey(t, Map(shopOnly(q)), "ledger").Metadata.Annotations[AnnotationInferredDependsOn]; got != comp("orders") {
		t.Errorf("restricted inferred-depends-on = %q", got)
	}
	// A runtime inferred call alone adds no annotation.
	h := newHand()
	h.svc("a", ct("a", "1.0.0"))
	h.svc("b", ct("b", "1.0.0"))
	h.snap.InferredRelationships = []fleet.Relationship{{FromService: key("a"), ToService: key("b"), To: "b", Type: fleet.RelationshipDependency,
		Provenance: fleet.ProvenanceInferred, Resolved: true, InferredSources: []fleet.InferredSource{{Source: "traces", Kind: "call"}}}}
	if a := byKey(t, h.es(), "a").Metadata.Annotations; a[AnnotationInferredDependsOn] != "" {
		t.Errorf("runtime call annotations %v", a)
	}
	if !slices.Contains(ownerAnnotations, AnnotationInferredOwner) {
		t.Error("an API copies the inferred owner with its Component's owner")
	}
}
