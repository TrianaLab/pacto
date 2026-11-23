package backstage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ranged is dep from@fromVer on to with the declared range and the fleet's
// range status and reason.
func ranged(from, fromVer, to, rng, status, reason string) fleet.Relationship {
	r := dep(from, fromVer, to, "")
	r.Compatibility, r.RangeStatus, r.RangeReason = rng, status, reason
	return r
}

// ranges decodes a's dependency-ranges annotation; ok is false when a has
// none.
func ranges(t *testing.T, es []Entity, a string) (map[string][]rangeFact, bool) {
	t.Helper()
	raw, ok := byKey(t, es, a).Metadata.Annotations[AnnotationDependencyRanges]
	if !ok {
		return nil, false
	}
	var m map[string][]rangeFact
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("dependency-ranges %q: %v", raw, err)
	}
	return m, true
}

func TestDependencyRangesFromDeclaredEdges(t *testing.T) {
	h := newHand()
	h.svc("checkout", ct("checkout", "1.0.0"))
	h.svc("payments", ct("payments", "2.1.0"))
	h.rel(ranged("checkout", "1.0.0", "payments", "^2", "satisfied", ""))
	want := `{"component:default/payments":[{"range":"^2","rangeStatus":"satisfied"}]}`
	if got := byKey(t, h.es(), "checkout").Metadata.Annotations[AnnotationDependencyRanges]; got != want {
		t.Errorf("dependency-ranges = %s, want %s", got, want)
	}
}

func TestDependencyRangesKeepCommasAndSpaces(t *testing.T) {
	h := newHand()
	h.svc("checkout", ct("checkout", "1.0.0"))
	h.svc("payments", ct("payments", "1.3.0"))
	h.rel(ranged("checkout", "1.0.0", "payments", ">=1.2.0, <2.0.0", "satisfied", ""))
	m, _ := ranges(t, h.es(), "checkout")
	if f := m[comp("payments")]; len(f) != 1 || f[0].Range != ">=1.2.0, <2.0.0" {
		t.Errorf("ranges = %+v", m)
	}
}

func TestDependencyRangesTwoRevisionsBothShown(t *testing.T) {
	h := newHand()
	h.svc("checkout", ct("checkout", "1.0.0"), ct("checkout", "2.0.0"))
	h.svc("payments", ct("payments", "2.0.0"))
	h.run("checkout", "1.0.0")
	h.run("checkout", "2.0.0")
	h.rel(ranged("checkout", "2.0.0", "payments", "^2", "satisfied", ""),
		ranged("checkout", "1.0.0", "payments", "^1", "unsatisfied", ""),
		// The same pair from a second edge is listed once.
		ranged("checkout", "2.0.0", "payments", "^2", "satisfied", ""))
	m, _ := ranges(t, h.es(), "checkout")
	want := []rangeFact{{Range: "^1", RangeStatus: "unsatisfied"}, {Range: "^2", RangeStatus: "satisfied"}}
	if f := m[comp("payments")]; len(f) != 2 || f[0] != want[0] || f[1] != want[1] {
		t.Errorf("ranges = %+v, want %+v", f, want)
	}
}

func TestDependencyRangesInferredProviderReason(t *testing.T) {
	h := newHand()
	h.svc("checkout", ct("checkout", "1.0.0"))
	h.svc("legacy", ct("legacy", "0.1.0"))
	h.rel(ranged("checkout", "1.0.0", "legacy", "^1", "unknown", fleet.RangeReasonInferredProvider))
	m, _ := ranges(t, h.es(), "checkout")
	if f := m[comp("legacy")]; len(f) != 1 || f[0] != (rangeFact{Range: "^1", RangeStatus: "unknown", RangeReason: fleet.RangeReasonInferredProvider}) {
		t.Errorf("ranges = %+v", m)
	}
}

func TestDependencyRangesAbsentWithoutRange(t *testing.T) {
	recent := fixed.Add(-time.Hour)
	h := newHand()
	h.svc("checkout", ct("checkout", "1.0.0"))
	h.svc("payments", ct("payments", "2.0.0"))
	h.svc("ledger", ct("ledger", "1.0.0"))
	// A declared edge with no range, an observed edge and an inferred edge
	// carrying a range: none is a declared range.
	inferred := fleet.Relationship{FromService: key("checkout"), ToService: key("ledger"), Type: fleet.RelationshipDependency,
		Provenance: fleet.ProvenanceInferred, Compatibility: "^1", RangeStatus: "satisfied"}
	h.rel(dep("checkout", "1.0.0", "payments", ""), observed("checkout", "ledger", &recent), inferred)
	if m, ok := ranges(t, h.es(), "checkout"); ok {
		t.Errorf("dependency-ranges = %+v", m)
	}
}

func TestDependencyRangesEmptyStatusReadsUnknown(t *testing.T) {
	h := newHand()
	h.svc("a", ct("a", "1.0.0"))
	h.external("stripe", "Stripe", fleet.ProvenanceDeclared)
	h.rel(fleet.Relationship{FromService: key("a"), FromRevision: rk("a", "1.0.0"), To: "stripe", ToExternal: "stripe",
		Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceDeclared, External: true, Compatibility: "^3"})
	want := `{"resource:default/Stripe":[{"range":"^3","rangeStatus":"unknown"}]}`
	if got := byKey(t, h.es(), "a").Metadata.Annotations[AnnotationDependencyRanges]; got != want {
		t.Errorf("dependency-ranges = %s, want %s", got, want)
	}
}

func TestDependencyRangesHiddenEdgeAbsent(t *testing.T) {
	front := rev("", "shopfront", "1.0.0", func(c *contract.Contract) {
		c.Dependencies = []contract.Dependency{{Name: "vaultkeeper", Required: true, Compatibility: "^1"}}
	})
	vault := rev("", "vaultkeeper", "1.0.0")
	ft, vt := target("shopfront", front.Digest, contract.Owner{}), target("vaultkeeper", vault.Digest, contract.Owner{})
	vt.Scope = "secret"
	snap := snapshot(t, &fleet.Collection{Revisions: []fleet.RawRevision{front, vault}, Targets: []fleet.RawTarget{ft, vt}})
	if m, ok := ranges(t, mapSnap(snap), "shopfront"); !ok || len(m) != 1 {
		t.Fatalf("the unrestricted export lacks the range: %+v", m)
	}
	es := Map(fleet.NewQuery(snap).Restrict(func(_, ns string) bool { return ns == "shop" || ns == "" }, nil))
	if m, ok := ranges(t, es, "shopfront"); ok {
		t.Errorf("a hidden edge leaks its range: %+v", m)
	}
	if got := byKey(t, es, "shopfront").Metadata.Annotations[AnnotationHiddenRelations]; got != "1" {
		t.Errorf("hidden-relations = %q", got)
	}
}

func TestConformanceFollowsRestrictedView(t *testing.T) {
	front := rev("", "shopfront", "1.0.0")
	ok, bad := target("shopfront", front.Digest, contract.Owner{}), target("shopfront", front.Digest, contract.Owner{})
	bad.Scope, bad.Name, bad.Compliance = "secret", "shopfront-secret", fleet.StatusNonCompliant
	snap := snapshot(t, &fleet.Collection{Revisions: []fleet.RawRevision{front}, Targets: []fleet.RawTarget{ok, bad}})
	if got := byKey(t, mapSnap(snap), "shopfront").Metadata.Annotations[AnnotationConformance]; got != fleet.StatusNonCompliant {
		t.Fatalf("unrestricted conformance = %q", got)
	}
	es := Map(fleet.NewQuery(snap).Restrict(func(_, ns string) bool { return ns == "shop" || ns == "" }, nil))
	if got := byKey(t, es, "shopfront").Metadata.Annotations[AnnotationConformance]; got != fleet.StatusCompliant {
		t.Errorf("restricted conformance = %q, want the visible target's", got)
	}
}

func TestConformanceNoTargetNeverCompliant(t *testing.T) {
	es := ctbMap(t, []fleet.RawRevision{rev(ctbRoot, "payments", "1.0.0")}, contributed("legacy-billing", "", fixed.Add(-time.Hour)))
	if got := byKey(t, es, string(fleet.NewServiceKeyDomain(ctbRoot, "payments"))).Metadata.Annotations[AnnotationConformance]; got != fleet.StatusNotEvaluated {
		t.Errorf("declared, no target: conformance = %q", got)
	}
	if got := byKey(t, es, ctbKey("legacy-billing")).Metadata.Annotations[AnnotationConformance]; got != fleet.StatusUnknown {
		t.Errorf("contributed, no target: conformance = %q", got)
	}
	// A service inferred from a workload alone has no computed status.
	inferred := inferredComponents(mapCols(t, &fleet.Collection{InferredServices: []fleet.RawInferredService{{Namespace: "shop", Kind: "Deployment", Name: "cart"}}}))
	if len(inferred) != 1 {
		t.Fatalf("%d inferred components, want 1", len(inferred))
	}
	if got := inferred[0].Metadata.Annotations[AnnotationConformance]; got != fleet.StatusUnknown {
		t.Errorf("workload-inferred, no target: conformance = %q", got)
	}
}
