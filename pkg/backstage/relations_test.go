package backstage

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// hand builds a snapshot record by record, for edges Build makes hard to
// reach (a revision-scoped edge, a resolved revision, an inferred dependency).
type hand struct{ snap *fleet.FleetSnapshot }

func newHand() *hand {
	return &hand{&fleet.FleetSnapshot{
		GeneratedAt: fixed, Completeness: fleet.CompletenessComplete,
		Services:  map[fleet.ServiceKey]*fleet.ServiceRecord{},
		Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{},
		Targets:   map[fleet.TargetKey]*fleet.TargetRecord{},
		Externals: map[fleet.ExternalKey]*fleet.ExternalRecord{},
	}}
}

func key(name string) fleet.ServiceKey { return fleet.NewServiceKey(name) }

func rk(name, version string) fleet.RevisionKey { return fleet.RevisionKey(name + "@" + version) }

// ct is a contract of name at version declaring ifaces.
func ct(name, version string, ifaces ...contract.Interface) *contract.Contract {
	return &contract.Contract{Service: contract.Service{Name: name, Version: version}, Interfaces: ifaces}
}

func iface(name, typ string) contract.Interface {
	return contract.Interface{Name: name, Type: typ, Ref: "interfaces/" + name + ".yaml"}
}

// svc adds service name with one revision per contract (none for a
// target-only service).
func (h *hand) svc(name string, cs ...*contract.Contract) *fleet.ServiceRecord {
	k := key(name)
	s := h.snap.Services[k]
	if s == nil {
		s = &fleet.ServiceRecord{Key: k, Name: name}
		h.snap.Services[k] = s
	}
	for _, c := range cs {
		r := rk(name, c.Service.Version)
		h.snap.Revisions[r] = &fleet.ContractRevision{Key: r, ServiceKey: k, Service: name, Version: c.Service.Version,
			Contract: c, ResolvedRef: "oci://r/" + name + ":" + c.Service.Version}
		s.Revisions = append(s.Revisions, r)
	}
	return s
}

// run adds a target of name running version.
func (h *hand) run(name, version string) {
	tk := fleet.TargetKey("t/" + name + "/" + version)
	h.snap.Targets[tk] = &fleet.TargetRecord{Key: tk, ContractRevision: rk(name, version)}
	s := h.snap.Services[key(name)]
	s.Targets = append(s.Targets, tk)
}

func (h *hand) rel(rs ...fleet.Relationship) {
	h.snap.Relationships = append(h.snap.Relationships, rs...)
}

func (h *hand) external(k fleet.ExternalKey, label string, provenances ...string) {
	x := &fleet.ExternalRecord{Key: k, Label: label, Class: "vendor-api"}
	for _, p := range provenances {
		x.Sources = append(x.Sources, fleet.ExternalSource{Provenance: p})
	}
	h.snap.Externals[k] = x
}

func (h *hand) es() []Entity { return Map(fleet.NewQuery(h.snap)) }

// dep is a declared, resolved dependency of from@fromVer on to, resolved to
// to@toVer (no resolved revision when toVer is empty).
func dep(from, fromVer, to, toVer string, requires ...string) fleet.Relationship {
	r := fleet.Relationship{FromService: key(from), FromRevision: rk(from, fromVer), To: to, ToService: key(to),
		Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceDeclared, Resolved: true}
	if toVer != "" {
		r.ResolvedRevision = rk(to, toVer)
	}
	for _, i := range requires {
		r.Requires = append(r.Requires, contract.Requirement{Interface: i})
	}
	return r
}

// unresolved is a declared dependency of from@fromVer on to that resolves to
// nothing.
func unresolved(from, fromVer, to string) fleet.Relationship {
	return fleet.Relationship{FromService: key(from), FromRevision: rk(from, fromVer), To: to,
		Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceDeclared}
}

func observed(from, to string, last *time.Time) fleet.Relationship {
	return fleet.Relationship{FromService: key(from), ToService: key(to), Type: fleet.RelationshipDependency,
		Provenance: fleet.ProvenanceObserved, Resolved: true, LastSeen: last}
}

func comp(name string) string { return componentRef(name) }

func TestRelationProvenanceTable(t *testing.T) {
	old := fixed.AddDate(0, -4, 0)
	recent := fixed.Add(-time.Hour)
	older := fixed.Add(-2 * time.Hour)
	h := newHand()
	h.svc("a", ct("a", "1.0.0"))
	for _, n := range []string{"b", "c", "d", "e"} {
		h.svc(n, ct(n, "1.0.0"))
	}
	h.rel(dep("a", "1.0.0", "b", ""), observed("a", "c", &old),
		dep("a", "1.0.0", "d", ""), observed("a", "d", &recent), observed("a", "d", &older),
		observed("a", "e", nil))
	e := byKey(t, h.es(), "a")
	if want := []string{comp("b"), comp("c"), comp("d"), comp("e")}; !slices.Equal(e.Spec.DependsOn, want) {
		t.Errorf("dependsOn = %v, want %v", e.Spec.DependsOn, want)
	}
	want := comp("b") + "=declared," + comp("c") + "=observed@" + old.Format(time.RFC3339) + "," +
		comp("d") + "=declared+observed@" + recent.Format(time.RFC3339) + "," + comp("e") + "=observed"
	if got := e.Metadata.Annotations[AnnotationRelationProvenance]; got != want {
		t.Errorf("relation-provenance =\n%s\nwant\n%s", got, want)
	}
}

func TestDependsOnFromCurrentRevisionOnly(t *testing.T) {
	build := func(running bool) Entity {
		h := newHand()
		h.svc("pay", ct("pay", "1.0.0"), ct("pay", "2.0.0"))
		h.svc("ledger", ct("ledger", "1.0.0", iface("rest", "openapi")))
		h.rel(dep("pay", "1.0.0", "ledger", "1.0.0", "rest"), unresolved("pay", "1.0.0", "ghost"))
		if running {
			h.run("pay", "1.0.0")
		}
		return byKey(t, h.es(), "pay")
	}
	idle := build(false)
	if len(idle.Spec.DependsOn)+len(idle.Spec.ConsumesAPIs) != 0 || idle.Metadata.Annotations[AnnotationRelationProvenance] != "" ||
		idle.Metadata.Annotations[AnnotationUnresolvedDependency] != "" || idle.Metadata.Annotations[AnnotationCompleteness] != "complete" {
		t.Errorf("an unused revision's edges were exported: %+v", idle)
	}
	live := build(true)
	if !slices.Equal(live.Spec.DependsOn, []string{comp("ledger")}) || !slices.Equal(live.Spec.ConsumesAPIs, []string{apiRef("ledger-rest")}) ||
		live.Metadata.Annotations[AnnotationUnresolvedDependency] != "1" {
		t.Errorf("the running revision's edges are missing: %+v", live)
	}
}

func TestInferredEdgeStaysOutOfSpec(t *testing.T) {
	h := newHand()
	h.svc("a", ct("a", "1.0.0"))
	h.svc("b", ct("b", "1.0.0"))
	h.svc("c", ct("c", "1.0.0"))
	h.external("stripe", "Stripe", fleet.ProvenanceInferred)
	inferred := func(r fleet.Relationship) fleet.Relationship {
		r.Provenance, r.Resolved, r.FromRevision = fleet.ProvenanceInferred, false, ""
		return r
	}
	h.rel(inferred(fleet.Relationship{FromService: key("a"), ToExternal: "stripe", Type: fleet.RelationshipDependency}),
		inferred(dep("a", "", "b", "")), inferred(dep("a", "", "c", "")), dep("a", "1.0.0", "c", ""))
	e := byKey(t, h.es(), "a")
	if !slices.Equal(e.Spec.DependsOn, []string{comp("c")}) {
		t.Errorf("dependsOn = %v", e.Spec.DependsOn)
	}
	if got := e.Metadata.Annotations[AnnotationInferredDependsOn]; got != comp("b")+",resource:default/Stripe" {
		t.Errorf("inferred-depends-on = %q", got)
	}
	assertInferredOutOfSpec(t, h.es())
	assertInferredOutOfSpec(t, mapCols(t, goldenSources()...))
}

// assertInferredOutOfSpec checks that no entity's inferred-depends-on ref is
// in its spec, and that the fixture holds at least one inferred ref.
func assertInferredOutOfSpec(t *testing.T, es []Entity) {
	t.Helper()
	seen := false
	for _, e := range es {
		for _, ref := range strings.Split(e.Metadata.Annotations[AnnotationInferredDependsOn], ",") {
			if ref == "" {
				continue
			}
			seen = true
			if slices.Contains(e.Spec.DependsOn, ref) || slices.Contains(e.Spec.ConsumesAPIs, ref) {
				t.Errorf("%s: inferred ref %s is in spec", e.Metadata.Name, ref)
			}
		}
	}
	if !seen {
		t.Error("no inferred ref in the fixture")
	}
}

func TestPolicyReferenceNotExported(t *testing.T) {
	h := newHand()
	h.svc("a", ct("a", "1.0.0"))
	h.svc("b", ct("b", "1.0.0"))
	r := dep("a", "1.0.0", "b", "")
	r.Type = fleet.RelationshipPolicyRef
	h.rel(r)
	if e := byKey(t, h.es(), "a"); len(e.Spec.DependsOn) != 0 || e.Metadata.Annotations[AnnotationRelationProvenance] != "" {
		t.Errorf("a policy reference was exported: %+v", e)
	}
}

func TestConfigReferenceIsDependsOn(t *testing.T) {
	h := newHand()
	h.svc("a", ct("a", "1.0.0"))
	h.svc("b", ct("b", "1.0.0"))
	r := dep("a", "1.0.0", "b", "1.0.0")
	r.Type = fleet.RelationshipConfigRef
	h.rel(r)
	e := byKey(t, h.es(), "a")
	if !slices.Equal(e.Spec.DependsOn, []string{comp("b")}) || e.Metadata.Annotations[AnnotationRelationProvenance] != comp("b")+"=declared" {
		t.Errorf("configuration reference: %+v", e)
	}
}

func TestDependsOnExternalResource(t *testing.T) {
	h := newHand()
	h.svc("a", ct("a", "1.0.0"))
	h.external("stripe", "Stripe", fleet.ProvenanceDeclared)
	h.rel(fleet.Relationship{FromService: key("a"), FromRevision: rk("a", "1.0.0"), To: "stripe", ToExternal: "stripe",
		Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceDeclared, External: true},
		// An external the view does not hold is named by nobody.
		fleet.Relationship{FromService: key("a"), ToExternal: "gone", Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceObserved})
	es := h.es()
	e := byKey(t, es, "a")
	if !slices.Equal(e.Spec.DependsOn, []string{"resource:default/Stripe"}) || e.Metadata.Annotations[AnnotationCompleteness] != "complete" ||
		e.Metadata.Annotations[AnnotationUnresolvedDependency] != "" {
		t.Errorf("external dependency: %+v", e)
	}
	r := ofKind(es, kindRes)
	if len(r) != 1 || r[0].Metadata.Name != "Stripe" || r[0].Spec.Type != "vendor-api" || r[0].Spec.Owner != unknown ||
		r[0].Metadata.Annotations[AnnotationExternalKey] != "stripe" || r[0].Metadata.Annotations[AnnotationProvenance] != "declared" {
		t.Errorf("resources = %+v", r)
	}
}

func TestInferredExternalResourceMarked(t *testing.T) {
	h := newHand()
	h.svc("a", ct("a", "1.0.0"))
	h.external("host/legacy.example", "", fleet.ProvenanceInferred)
	h.rel(fleet.Relationship{FromService: key("a"), ToExternal: "host/legacy.example", Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceInferred})
	es := h.es()
	r := ofKind(es, kindRes)
	if len(r) != 1 || r[0].Metadata.Name != "host-legacy.example" || r[0].Metadata.Annotations[AnnotationProvenance] != "inferred" ||
		!slices.Equal(r[0].Metadata.Tags, []string{TagInferred}) {
		t.Fatalf("resources = %+v", r)
	}
	for _, e := range es {
		if slices.Contains(e.Spec.DependsOn, "resource:default/"+r[0].Metadata.Name) {
			t.Errorf("%s depends on an inferred external", e.Metadata.Name)
		}
	}
}

func TestResourceProvenanceUnion(t *testing.T) {
	for _, tc := range []struct {
		sources []string
		want    string
	}{
		{[]string{"declared", "observed", "inferred"}, "declared+observed"},
		{[]string{"inferred", "declared"}, "declared"},
		{[]string{"observed", "inferred"}, "observed"},
		{nil, "inferred"},
	} {
		h := newHand()
		h.external("x", "x", tc.sources...)
		r := ofKind(h.es(), kindRes)[0]
		if got := r.Metadata.Annotations[AnnotationProvenance]; got != tc.want || (len(r.Metadata.Tags) > 0) != (tc.want == "inferred") {
			t.Errorf("%v: provenance %q tags %v, want %q", tc.sources, got, r.Metadata.Tags, tc.want)
		}
	}
}

func TestAPIPerInterface(t *testing.T) {
	events := iface("events", "asyncapi")
	events.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2026-09-01"}
	h := newHand()
	h.svc("pay", ct("pay", "1.0.0", iface("rest", "openapi"), events))
	es := h.es()
	c := byKey(t, es, "pay")
	if want := []string{apiRef("pay-events"), apiRef("pay-rest")}; !slices.Equal(c.Spec.ProvidesAPIs, want) {
		t.Errorf("providesApis = %v", c.Spec.ProvidesAPIs)
	}
	apis := ofKind(es, kindAPI)
	if len(apis) != 2 {
		t.Fatalf("apis = %+v", apis)
	}
	for i, want := range []struct{ name, typ, lifecycle, ref string }{
		{"pay-events", "asyncapi", "deprecated", "interfaces/events.yaml"},
		{"pay-rest", "openapi", unknown, "interfaces/rest.yaml"},
	} {
		a := apis[i]
		if a.Metadata.Name != want.name || a.Spec.Type != want.typ || a.Spec.Lifecycle != want.lifecycle || a.Spec.Owner != c.Spec.Owner ||
			a.Spec.Definition != "Defined by "+want.ref+" in oci://r/pay:1.0.0. Read it with pacto pull." ||
			a.Metadata.Annotations[AnnotationServiceKey] != "pay" || a.Metadata.Annotations[AnnotationProvenance] != "declared" {
			t.Errorf("api %d = %+v", i, a)
		}
	}

	unreadable := newHand()
	unreadable.svc("pay", ct("pay", "1.0.0", iface("rest", "openapi")))
	unreadable.run("pay", "1.0.0")
	unreadable.snap.Revisions[rk("pay", "1.0.0")].Contract = nil
	targetOnly := newHand()
	targetOnly.svc("pay")
	for name, h := range map[string]*hand{"unreadable": unreadable, "target only": targetOnly} {
		es := h.es()
		if len(ofKind(es, kindAPI)) != 0 || len(byKey(t, es, "pay").Spec.ProvidesAPIs) != 0 {
			t.Errorf("%s: exported an API", name)
		}
	}
}

// pacto pull only reads oci:// refs: a scheme-less remote ref gains it, and a
// local revision names its key with no pull instruction.
func TestAPIDefinitionRef(t *testing.T) {
	for ref, want := range map[string]string{
		"ghcr.io/acme/pay@sha256:1":       "Defined by interfaces/rest.yaml in oci://ghcr.io/acme/pay@sha256:1. Read it with pacto pull.",
		"oci://ghcr.io/acme/pay@sha256:1": "Defined by interfaces/rest.yaml in oci://ghcr.io/acme/pay@sha256:1. Read it with pacto pull.",
		"":                                "Defined by interfaces/rest.yaml in revision " + string(rk("pay", "1.0.0")) + ".",
	} {
		h := newHand()
		h.svc("pay", ct("pay", "1.0.0", iface("rest", "openapi")))
		h.snap.Revisions[rk("pay", "1.0.0")].ResolvedRef = ref
		if got := ofKind(h.es(), kindAPI)[0].Spec.Definition; got != want {
			t.Errorf("ref %q: definition %q, want %q", ref, got, want)
		}
	}
}

func TestConsumesAPIsFromRequires(t *testing.T) {
	rest := iface("rest", "openapi")
	for _, tc := range []struct {
		name string
		mut  func(*hand)
		want []string
	}{
		{"required and declared", func(h *hand) {
			h.svc("ledger", ct("ledger", "1.0.0", rest))
			h.rel(dep("pay", "1.0.0", "ledger", "", "rest"))
		}, []string{apiRef("ledger-rest")}},
		{"required, not declared", func(h *hand) {
			h.svc("ledger", ct("ledger", "1.0.0", rest))
			h.rel(dep("pay", "1.0.0", "ledger", "", "grpc"))
		}, nil},
		{"resolved revision lacks it, current has it", func(h *hand) {
			h.svc("ledger", ct("ledger", "1.0.0"), ct("ledger", "2.0.0", rest))
			h.rel(dep("pay", "1.0.0", "ledger", "1.0.0", "rest"))
		}, nil},
		// Only the current revision's interfaces become API entities, so a ref
		// to rest would point at an entity the file does not hold.
		{"resolved revision has it, current lacks it", func(h *hand) {
			h.svc("ledger", ct("ledger", "1.0.0", rest), ct("ledger", "2.0.0"))
			h.rel(dep("pay", "1.0.0", "ledger", "1.0.0", "rest"))
		}, nil},
		{"resolved older revision and current both have it", func(h *hand) {
			h.svc("ledger", ct("ledger", "1.0.0", rest), ct("ledger", "2.0.0", rest))
			h.rel(dep("pay", "1.0.0", "ledger", "1.0.0", "rest"))
		}, []string{apiRef("ledger-rest")}},
		{"resolved revision unreadable", func(h *hand) {
			h.svc("ledger", ct("ledger", "1.0.0", rest))
			h.snap.Revisions[rk("ledger", "1.0.0")].Contract = nil
			h.rel(dep("pay", "1.0.0", "ledger", "1.0.0", "rest"))
		}, nil},
		{"current revision unreadable", func(h *hand) {
			h.svc("ledger", ct("ledger", "1.0.0", rest))
			h.run("ledger", "1.0.0")
			h.snap.Revisions[rk("ledger", "1.0.0")].Contract = nil
			h.rel(dep("pay", "1.0.0", "ledger", "", "rest"))
		}, nil},
	} {
		h := newHand()
		h.svc("pay", ct("pay", "1.0.0"))
		tc.mut(h)
		e := byKey(t, h.es(), "pay")
		if !slices.Equal(e.Spec.ConsumesAPIs, tc.want) || !slices.Equal(e.Spec.DependsOn, []string{comp("ledger")}) {
			t.Errorf("%s: consumesApis %v dependsOn %v, want %v", tc.name, e.Spec.ConsumesAPIs, e.Spec.DependsOn, tc.want)
		}
	}
}

// A golden path is never exported, so a dependency on one names no API: a
// ref to it would be the dangling api:default/.
func TestConsumesAPIsSkipsGoldenPathProvider(t *testing.T) {
	h := newHand()
	h.svc("pay", ct("pay", "1.0.0"))
	h.svc("starter", ct("starter", "1.0.0", iface("rest", "openapi"))).Template = true
	h.rel(dep("pay", "1.0.0", "starter", "", "rest"))
	e := byKey(t, h.es(), "pay")
	if len(e.Spec.ConsumesAPIs) != 0 || len(e.Spec.DependsOn) != 0 {
		t.Errorf("consumesApis %v dependsOn %v, want none", e.Spec.ConsumesAPIs, e.Spec.DependsOn)
	}
}

func TestObservedCallerStub(t *testing.T) {
	h := newHand()
	h.svc("a", ct("a", "1.0.0"))
	h.svc("b", ct("b", "1.0.0"))
	h.svc("starter", ct("starter", "1.0.0")).Template = true
	const caller = "k8s:shop/Deployment/legacy"
	h.snap.UncontractedCallers = []fleet.UncontractedCaller{
		{Caller: caller, ToService: key("b")}, {Caller: caller, ToService: key("a")}, {Caller: caller, ToService: key("a")},
		{Caller: caller, ToService: key("starter")}, {Caller: "k8s:shop/Deployment/lonely", ToService: key("starter")},
	}
	var stubs []Entity
	for _, e := range ofKind(h.es(), kindComp) {
		if e.Metadata.Annotations[AnnotationCaller] != "" {
			stubs = append(stubs, e)
		}
	}
	if len(stubs) != 2 {
		t.Fatalf("stubs = %+v", stubs)
	}
	assertStub(t, stubs[0], caller)
	if lonely := stubs[1]; len(lonely.Spec.DependsOn) != 0 || lonely.Metadata.Annotations[AnnotationRelationProvenance] != "" {
		t.Errorf("a stub names a golden path: %+v", lonely)
	}
}

// assertStub checks s is the observed stub of caller, calling a and b.
func assertStub(t *testing.T, s Entity, caller string) {
	t.Helper()
	ann := s.Metadata.Annotations
	if s.Metadata.Name != "legacy-"+sha256Hex(caller)[:8] || ann[AnnotationCaller] != caller || ann[AnnotationProvenance] != "observed" ||
		!slices.Equal(s.Metadata.Tags, []string{TagObserved}) || ann[AnnotationAsOf] == "" {
		t.Errorf("stub metadata = %+v", s.Metadata)
	}
	if s.Spec.Owner != unknown || s.Spec.Lifecycle != unknown || s.Spec.Type != "service" ||
		!slices.Equal(s.Spec.DependsOn, []string{comp("a"), comp("b")}) {
		t.Errorf("stub spec = %+v", s.Spec)
	}
	if ann[AnnotationRelationProvenance] != comp("a")+"=observed,"+comp("b")+"=observed" ||
		ann[AnnotationCompleteness] != "complete" || ann[AnnotationCallerAmbiguous] != "" {
		t.Errorf("stub annotations = %v", ann)
	}
}

func TestAmbiguousCallerStubPartial(t *testing.T) {
	h := newHand()
	h.svc("a", ct("a", "1.0.0"))
	h.snap.UncontractedCallers = []fleet.UncontractedCaller{{Caller: "web", ToService: key("a"), Ambiguous: true}}
	s := ofKind(h.es(), kindComp)[1]
	if s.Metadata.Annotations[AnnotationCallerAmbiguous] != "true" || s.Metadata.Annotations[AnnotationCompleteness] != "partial" {
		t.Errorf("ambiguous stub = %+v", s)
	}
}

func TestStubNeverTakesServiceName(t *testing.T) {
	h := newHand()
	h.svc("legacy", ct("legacy", "1.0.0"))
	h.svc("a", ct("a", "1.0.0"))
	const caller = "k8s:shop/Deployment/legacy"
	h.snap.UncontractedCallers = []fleet.UncontractedCaller{{Caller: caller, ToService: key("a")}}
	es := h.es()
	if n := byKey(t, es, "legacy").Metadata.Name; n != "legacy" {
		t.Errorf("service name = %q", n)
	}
	for _, e := range es {
		if e.Metadata.Annotations[AnnotationCaller] == caller && e.Metadata.Name != "legacy-"+sha256Hex(caller)[:8] {
			t.Errorf("stub name = %q", e.Metadata.Name)
		}
	}
}

func TestMapRestrictedViewNamesNoHidden(t *testing.T) {
	front := rev("", "shopfront", "1.0.0", func(c *contract.Contract) {
		c.Dependencies = []contract.Dependency{{Name: "vaultkeeper", Required: true}}
	})
	vault := rev("", "vaultkeeper", "1.0.0", func(c *contract.Contract) {
		c.Interfaces = []contract.Interface{iface("rest", "openapi")}
	})
	ft, vt := target("shopfront", front.Digest, contract.Owner{}), target("vaultkeeper", vault.Digest, contract.Owner{})
	vt.Scope = "secret"
	snap := snapshot(t, &fleet.Collection{Revisions: []fleet.RawRevision{front, vault}, Targets: []fleet.RawTarget{ft, vt}})
	full := encode(t, mapSnap(snap))
	if !strings.Contains(full, "vaultkeeper") {
		t.Fatalf("the unrestricted export lacks the provider:\n%s", full)
	}
	view := fleet.NewQuery(snap).Restrict(func(ns string) bool { return ns == "shop" || ns == "" }, nil)
	es := Map(view)
	if out := encode(t, es); strings.Contains(out, "vaultkeeper") {
		t.Errorf("the restricted export names a hidden service:\n%s", out)
	}
	e := byKey(t, es, "shopfront")
	if e.Metadata.Annotations[AnnotationHiddenRelations] != "1" || e.Metadata.Annotations[AnnotationCompleteness] != "partial" {
		t.Errorf("shopfront = %+v", e)
	}
}

func TestCompletenessPerEntity(t *testing.T) {
	missing := newHand()
	missing.svc("a", ct("a", "1.0.0"))
	missing.rel(unresolved("a", "1.0.0", "ghost"))

	twice := newHand()
	twice.svc("a", ct("a", "1.0.0"), ct("a", "2.0.0"))
	twice.run("a", "1.0.0")
	twice.run("a", "2.0.0")
	twice.rel(unresolved("a", "1.0.0", "ghost"), unresolved("a", "2.0.0", "ghost"))

	external := newHand()
	external.svc("a", ct("a", "1.0.0"))
	ext := unresolved("a", "1.0.0", "stripe")
	ext.External = true
	external.rel(ext)

	for _, tc := range []struct {
		name               string
		h                  *hand
		completeness, want string
	}{
		{"unresolved", missing, "partial", "1"},
		{"two revisions, one provider", twice, "partial", "1"},
		{"external", external, "complete", ""},
	} {
		ann := byKey(t, tc.h.es(), "a").Metadata.Annotations
		if ann[AnnotationCompleteness] != tc.completeness || ann[AnnotationUnresolvedDependency] != tc.want {
			t.Errorf("%s: %v", tc.name, ann)
		}
	}

	partial := snapshot(t, goldenSources()...)
	partial.Completeness = fleet.CompletenessPartial
	es := mapSnap(partial)
	kinds := map[string]bool{}
	for _, e := range es {
		kinds[e.Kind] = true
		if e.Metadata.Annotations[AnnotationCompleteness] != "partial" {
			t.Errorf("%s %s is %s in a partial snapshot", e.Kind, e.Metadata.Name, e.Metadata.Annotations[AnnotationCompleteness])
		}
	}
	if len(kinds) != 4 {
		t.Errorf("the golden fixture lacks a kind: %v", kinds)
	}
}
