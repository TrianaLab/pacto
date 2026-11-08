package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func revKeyOf(t *testing.T, q *Query, service string) string {
	t.Helper()
	view, err := q.GetService(service)
	if err != nil {
		t.Fatalf("GetService(%q): %v", service, err)
	}
	return string(view.Revisions[0].Key)
}

func TestEntityDetail_Service(t *testing.T) {
	q := productFleet(t)
	d, err := q.EntityDetail(KindService, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if d.Meta.SchemaVersion != ProductSchemaVersion || d.Entity.Kind != KindService {
		t.Errorf("envelope wrong: %+v", d.Entity)
	}
	if d.Service == nil || payloadCount(d) != 1 {
		t.Fatalf("exactly the service payload must be populated: %+v", d)
	}
	s := d.Service
	if s.Deployments.Count != 2 {
		t.Errorf("deployments = %d, want 2", s.Deployments.Count)
	}
	if s.Dependencies.Total < 1 {
		t.Errorf("dependencies = %d, want >= 1", s.Dependencies.Total)
	}
	// alpha->leaf declared+observed and beta->alpha observed shadow.
	if s.Relationships.Count != 2 {
		t.Errorf("relationships = %d, want 2", s.Relationships.Count)
	}
	if s.Evidence.Count != 2 {
		t.Errorf("evidence = %d, want 2 (alpha-app + alpha-ancient)", s.Evidence.Count)
	}
	if !ownershipIs(s.Ownership, "team-a") {
		t.Errorf("ownership = %+v", s.Ownership)
	}
	if len(d.Actions) != 3 {
		t.Errorf("actions = %v", d.Actions)
	}
}

// TestRevisionRef_CarriesVersion proves a revision reference carries its declared version
// EXPLICITLY, so a consumer (the legacy version-bookmark migration) can
// match a requested version to a canonical RevisionKey without parsing it out of a display
// label.
func TestRevisionRef_CarriesVersion(t *testing.T) {
	q := productFleet(t)
	d, err := q.EntityDetail(KindService, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if d.Service == nil || len(d.Service.Revisions.Items) == 0 {
		t.Fatalf("service must carry revisions: %+v", d.Service)
	}
	rev := d.Service.Revisions.Items[0]
	if rev.Kind != KindRevision {
		t.Fatalf("revisions[0] kind = %q, want revision", rev.Kind)
	}
	if rev.Version != "1.0.0" {
		t.Errorf("revision ref version = %q, want 1.0.0 (explicit, not parsed from the label)", rev.Version)
	}
}

func TestEntityDetail_ServiceFindingsAttributed(t *testing.T) {
	q := productFleet(t)
	beta, err := q.EntityDetail(KindService, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if beta.Service == nil || beta.Service.Findings.Count != 1 {
		t.Fatalf("beta findings = %+v, want 1", beta.Service)
	}
	// A finding aggregated at the service level must carry the affected target ref.
	af := beta.Service.Findings.Items[0]
	if af.Entity.Kind != KindTarget || af.Entity.Key == "" {
		t.Errorf("service finding must be attributed to its target: %+v", af.Entity)
	}

	if _, err := q.EntityDetail(KindService, "ghostly"); err == nil {
		t.Error("missing service must error")
	}
	amb := NewQuery(twoDomainSnap(t))
	if _, err := amb.EntityDetail(KindService, "shared"); err == nil {
		t.Error("ambiguous service must error")
	}
}

func TestEntityDetail_Revision(t *testing.T) {
	q := productFleet(t)

	// leaf revision: has tools (from its OpenAPI) and an inferred target link.
	leafRev := revKeyOf(t, q, "leaf-svc")
	d, err := q.EntityDetail(KindRevision, leafRev)
	if err != nil {
		t.Fatal(err)
	}
	if d.Entity.Kind != KindRevision || d.Revision == nil {
		t.Fatalf("kind/payload wrong: %+v", d.Entity)
	}
	if d.Revision.Tools.Count == 0 {
		t.Errorf("expected tools, got %+v", d.Revision.Tools)
	}
	if d.Revision.InferredTargets.Count != 1 {
		t.Errorf("expected 1 inferred target, got %+v", d.Revision.InferredTargets)
	}
	if d.Revision.Service.Kind != KindService {
		t.Errorf("revision must reference its parent service: %+v", d.Revision.Service)
	}

	// alpha revision: one declared edge (leaf), exact targets, valid.
	alphaRev := revKeyOf(t, q, "alpha")
	da, _ := q.EntityDetail(KindRevision, alphaRev)
	if da.Revision.Dependencies.Count != 1 {
		t.Errorf("alpha revision edges = %d, want 1", da.Revision.Dependencies.Count)
	}
	if da.Revision.ExactTargets.Count != 2 {
		t.Errorf("expected 2 exact targets, got %+v", da.Revision.ExactTargets)
	}
	if da.Status != "" {
		t.Errorf("valid revision status = %q, want empty", da.Status)
	}

	// invalid revision: Invalid status.
	badRev := revKeyOf(t, q, "bad-svc")
	db, _ := q.EntityDetail(KindRevision, badRev)
	if db.Status != StatusInvalid {
		t.Errorf("invalid revision status = %q, want Invalid", db.Status)
	}

	if _, err := q.EntityDetail(KindRevision, "nope@x"); err == nil {
		t.Error("missing revision must error")
	}
}

func TestEntityDetail_TargetExact(t *testing.T) {
	q := productFleet(t)
	d, err := q.EntityDetail(KindTarget, string(NewTargetKey("prod", "k8s", "alpha-app")))
	if err != nil {
		t.Fatal(err)
	}
	if d.Target == nil || payloadCount(d) != 1 {
		t.Fatalf("exactly the target payload must be populated: %+v", d)
	}
	tg := d.Target
	if tg.Coverage == nil || tg.ObservedRuntime.Count == 0 || tg.EvidenceAt == nil {
		t.Errorf("alpha-app must carry coverage + observedRuntime + evidence: %+v", tg)
	}
	if tg.Revision == nil || tg.Service.Kind != KindService || tg.Source == "" {
		t.Errorf("alpha-app must carry service ref, revision link and source: %+v", tg)
	}
	if tg.LinkState != "exact" && tg.LinkState != "inferred" {
		t.Errorf("alpha-app link state = %q, want exact/inferred", tg.LinkState)
	}
}

func TestEntityDetail_TargetAmbiguous(t *testing.T) {
	q := productFleet(t)
	// beta-app: ambiguous (no revision link), has a finding.
	db, err := q.EntityDetail(KindTarget, string(NewTargetKey("prod", "k8s", "beta-app")))
	if err != nil {
		t.Fatal(err)
	}
	if db.Target.Findings.Count != 1 {
		t.Errorf("beta-app findings = %d, want 1", db.Target.Findings.Count)
	}
	if db.Target.Revision != nil {
		t.Error("ambiguous target must not link to a revision")
	}
	if db.Target.LinkState != "ambiguous" {
		t.Errorf("beta-app link state = %q, want ambiguous", db.Target.LinkState)
	}
	if !hasLimitation(db.Target.Limitations.Items, LimitationRevisionAmbiguous) {
		t.Errorf("beta-app must carry the ambiguous limitation: %+v", db.Target.Limitations)
	}
}

func TestEntityDetail_TargetUnresolved(t *testing.T) {
	q := productFleet(t)
	// solo-app: unresolved, no evidence.
	ds, err := q.EntityDetail(KindTarget, string(NewTargetKey("prod", "k8s", "solo-app")))
	if err != nil {
		t.Fatal(err)
	}
	if ds.Target.EvidenceAt != nil {
		t.Errorf("solo-app must have no evidence time: %v", ds.Target.EvidenceAt)
	}
	if ds.Target.LinkState != "unresolved" {
		t.Errorf("solo-app link state = %q, want unresolved", ds.Target.LinkState)
	}
	if _, err := q.EntityDetail(KindTarget, "prod/k8s/nope"); err == nil {
		t.Error("missing target must error")
	}
}

func TestEntityDetail_Owner(t *testing.T) {
	q := productFleet(t)
	d, err := q.EntityDetail(KindOwner, "team:team-a")
	if err != nil {
		t.Fatal(err)
	}
	if d.Entity.Kind != KindOwner || d.Owner == nil {
		t.Fatalf("kind/payload wrong: %+v", d.Entity)
	}
	if d.Owner.Services.Count != 1 || d.Owner.Services.Items[0].Key != "alpha" {
		t.Errorf("owner services = %+v", d.Owner.Services)
	}
	if d.Owner.Revisions.Count != 1 {
		t.Errorf("owner revisions = %+v", d.Owner.Revisions)
	}

	if _, err := q.EntityDetail(KindOwner, "team:nobody"); err == nil {
		t.Error("unknown owner must error")
	}
}

func TestEntityDetail_Source(t *testing.T) {
	q := productFleet(t)

	local, err := q.EntityDetail(KindSource, "local")
	if err != nil {
		t.Fatal(err)
	}
	if local.Source == nil || local.Status != string(SourceAvailable) {
		t.Errorf("local status = %q payload=%+v", local.Status, local.Source)
	}
	if local.Source.LastSuccessfulSync == nil {
		t.Error("available source must report last successful sync")
	}
	if local.Source.Entities.Count == 0 {
		t.Error("local source must list contributed entities")
	}

	down, _ := q.EntityDetail(KindSource, "down")
	if down.Status != string(SourceUnavailable) {
		t.Errorf("down status = %q", down.Status)
	}
	if down.Source.Error == nil {
		t.Error("unavailable source must report an error")
	}
	if down.Source.LastSuccessfulSync != nil {
		t.Error("unavailable source must not claim a successful sync")
	}

	if _, err := q.EntityDetail(KindSource, "ghost-source"); err == nil {
		t.Error("unknown source must error")
	}
}

func TestEntityDetail_UnknownKind(t *testing.T) {
	q := productFleet(t)
	if _, err := q.EntityDetail("weird", "x"); err == nil {
		t.Error("unknown kind must error")
	}
}

func TestEntityDetail_ChainRevisionAndService(t *testing.T) {
	q := chainFleet(t)

	// The api revision declares db + cache, so its dependency edge list has two
	// entries and the edge-ordering comparator runs.
	apiRev := revKeyOf(t, q, "api")
	d, err := q.EntityDetail(KindRevision, apiRev)
	if err != nil {
		t.Fatal(err)
	}
	if d.Revision.Dependencies.Count != 2 {
		t.Errorf("api revision edges = %d, want 2", d.Revision.Dependencies.Count)
	}

	// The api service has a declared dependent (web), so serviceKeyRefs iterates.
	ds, err := q.EntityDetail(KindService, "api")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Service.Dependents.Count != 1 || ds.Service.Dependents.Items[0].Key != "web" {
		t.Errorf("api dependents = %+v", ds.Service.Dependents)
	}
}

func TestServiceOwnership(t *testing.T) {
	s := &ServiceRecord{Key: "svc", Owner: contract.Owner{Team: "team-x"}}
	revs := []*ContractRevision{
		{Key: "svc@1", Owner: contract.Owner{Team: "team-x"}}, // matches: no conflict
		{Key: "svc@2", Owner: contract.Owner{Team: "team-y"}}, // conflict
		{Key: "svc@3"}, // empty owner: skipped
	}
	info := serviceOwnership(s, revs)
	if info.Owner != "team-x" || info.Ref == nil {
		t.Fatalf("owner info = %+v", info)
	}
	if info.Conflicts.Total != 1 || info.Conflicts.Count != 1 || info.Conflicts.Truncated ||
		len(info.Conflicts.Items) != 1 || !strings.Contains(info.Conflicts.Items[0], "team-y") {
		t.Errorf("conflicts = %+v", info.Conflicts)
	}

	empty := serviceOwnership(&ServiceRecord{Key: "svc2"}, nil)
	if empty.Owner != "" || empty.Ref != nil {
		t.Errorf("ownerless service must have no owner ref: %+v", empty)
	}
}

// TestServiceRelationships_OnlyEdgesIncidentToTheService proves the entity's relationship
// list is about the ENTITY. A neighborhood is a graph, so at depth 1 it already carries
// edges between two neighbours; rendered in a flat list keyed on the counterpart, those
// read as the entity's own relationships and repeat a neighbour's name once per edge it
// happens to have. Here `edge` depends on BOTH `hub` and `store`, so the hub
// neighborhood contains edge->store, which is not one of hub's relationships.
func TestServiceRelationships_OnlyEdgesIncidentToTheService(t *testing.T) {
	svc := func(name string, deps ...string) *contract.Contract {
		c := &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
			Workload:     contract.WorkloadService,
		}
		for _, d := range deps {
			c.Dependencies = append(c.Dependencies, contract.Dependency{Name: d, Ref: "oci://x/" + d, Required: true})
		}
		return c
	}
	local := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: svc("hub", "store"), FS: fstest.MapFS{}}, Digest: "sha256:hub"},
			{Bundle: &contract.Bundle{Contract: svc("store"), FS: fstest.MapFS{}}, Digest: "sha256:store"},
			// The neighbour that also talks to hub's dependency.
			{Bundle: &contract.Bundle{Contract: svc("edge", "hub", "store"), FS: fstest.MapFS{}}, Digest: "sha256:edge"},
		},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: time.Hour}, local)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	d, err := NewQuery(snap).EntityDetail(KindService, "hub")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range d.Service.Relationships.Items {
		got = append(got, e.From.Key+"->"+e.To.Key)
	}
	want := []string{"edge->hub", "hub->store"}
	if len(got) != len(want) {
		t.Fatalf("relationships = %v, want exactly %v (an edge between two neighbours is not hub's)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("relationship[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if d.Service.Relationships.Total == nil || *d.Service.Relationships.Total != len(want) {
		t.Errorf("total = %v, want %d: the total must count what the list counts", d.Service.Relationships.Total, len(want))
	}
}

// A revision page is FINE-GRAINED scope, and observation is recorded per SERVICE
// (build.go reconciles by the from/to service pair). So a revision's declared
// dependency gets no edge-scope declared-vs-observed verdict at all: the
// service-scoped fact travels as ObservationScope + ServiceCorroboration, exactly as
// projection.go's dependencyEdge already does for the same relationship in the graph.
// Emitting a Difference here instead both over-claims (a verdict about an edge nobody
// observed at that granularity) and contradicts itself -- "expected, not observed"
// beside a claim whose own reconciliation says matched.
func TestRevisionDetail_DependencyCarriesCorroborationNotAnEdgeVerdict(t *testing.T) {
	q := obsScopeFleet(t) // a@1.0.0 declares b; service-level telemetry observes a->b
	d, err := q.EntityDetail(KindRevision, string(revKeyForVersion(t, q, "a", "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	if d.Revision.Dependencies.Count != 1 {
		t.Fatalf("dependencies = %+v, want exactly 1", d.Revision.Dependencies)
	}
	e := d.Revision.Dependencies.Items[0]
	if e.Difference != "" {
		t.Errorf("Difference = %q, want empty: a revision edge has no edge-scope verdict", e.Difference)
	}
	if e.ObservationScope != ObservationScopeService {
		t.Errorf("ObservationScope = %q, want %q", e.ObservationScope, ObservationScopeService)
	}
	if e.ServiceCorroboration != CorroborationMatched {
		t.Errorf("ServiceCorroboration = %q, want %q: the service edge WAS corroborated", e.ServiceCorroboration, CorroborationMatched)
	}
	if e.Observed {
		t.Error("Observed = true: service telemetry never proves this specific revision edge")
	}
}

// detailFleet is a hand-built snapshot of one service "svc" with the given
// revisions and targets, each stamped with the service key.
func detailFleet(owner contract.Owner, revs []*ContractRevision, targets []*TargetRecord) *Query {
	const key = ServiceKey("svc")
	s := &ServiceRecord{Key: key, Name: "svc", Owner: owner}
	snap := &FleetSnapshot{Services: map[ServiceKey]*ServiceRecord{key: s}, Revisions: map[RevisionKey]*ContractRevision{}, Targets: map[TargetKey]*TargetRecord{}}
	for _, r := range revs {
		r.Service, r.ServiceKey = "svc", key
		snap.Revisions[r.Key] = r
		s.Revisions = append(s.Revisions, r.Key)
	}
	for _, t := range targets {
		t.ServiceKey = key
		snap.Targets[t.Key] = t
		s.Targets = append(s.Targets, t.Key)
	}
	return NewQuery(snap)
}

func serviceDetailOf(t *testing.T, q *Query) *ServiceDetailData {
	t.Helper()
	d, err := q.EntityDetail(KindService, "svc")
	if err != nil {
		t.Fatal(err)
	}
	return d.Service
}

// TestServiceOwnershipUnknown: with no readable contract and no source that
// reads the contract a target runs, Pacto cannot tell whether an owner is
// declared, which is different from no declared owner.
func TestServiceOwnershipUnknown(t *testing.T) {
	readable := func() *contract.Contract { return &contract.Contract{Service: contract.Service{Name: "svc"}} }
	cases := []struct {
		name            string
		owner           contract.Owner
		rev             *ContractRevision
		domainKnown     bool
		declared, unkwn bool
	}{
		{"unread contract", contract.Owner{}, &ContractRevision{Key: "svc@1"}, false, false, true},
		{"invalid contract", contract.Owner{}, &ContractRevision{Key: "svc@1", Contract: readable(), validated: true}, false, false, true},
		{"target-reported owner", contract.Owner{Team: "ops"}, &ContractRevision{Key: "svc@1"}, false, true, false},
		{"valid contract without owner", contract.Owner{}, &ContractRevision{Key: "svc@1", Contract: readable(), validated: true, Valid: true}, false, false, false},
		{"target contract read", contract.Owner{}, &ContractRevision{Key: "svc@1"}, true, false, false},
		{"invalid domain slug", contract.Owner{}, &ContractRevision{Key: "svc@1", Contract: &contract.Contract{Service: contract.Service{Name: "svc", Domain: "Not A Slug"}}}, false, false, false},
	}
	for _, c := range cases {
		q := detailFleet(c.owner, []*ContractRevision{c.rev}, []*TargetRecord{{Key: "t1", Name: "t1", domainKnown: c.domainKnown}})
		if o := serviceDetailOf(t, q).Ownership; o.Declared != c.declared || o.Unknown != c.unkwn {
			t.Errorf("%s: declared = %v, unknown = %v, want %v/%v", c.name, o.Declared, o.Unknown, c.declared, c.unkwn)
		}
	}
}

func TestServiceDetailOwnerRevision(t *testing.T) {
	q := detailFleet(contract.Owner{Team: "team-a"}, []*ContractRevision{
		{Key: "svc@1", Version: "1.0.0", Owner: contract.Owner{Team: "team-a"}},
		{Key: "svc@2", Version: "2.0.0"},
	}, nil)
	if r := serviceDetailOf(t, q).OwnerRevision; r == nil || r.Key != "svc@1" {
		t.Errorf("owner revision = %+v, want svc@1", r)
	}
	q = detailFleet(contract.Owner{}, []*ContractRevision{{Key: "svc@1", Version: "1.0.0"}}, nil)
	if r := serviceDetailOf(t, q).OwnerRevision; r != nil {
		t.Errorf("owner revision = %+v, want none", r)
	}
}

func TestServiceDetailCapabilities(t *testing.T) {
	c := &contract.Contract{Capabilities: []contract.Capability{{Type: "metrics"}, {Ref: "oci://x/ext"}, {Type: "health"}}}
	q := detailFleet(contract.Owner{}, []*ContractRevision{{Key: "svc@1", Contract: c}}, nil)
	if got := serviceDetailOf(t, q).Capabilities; got.Total != 2 || !slices.Equal(got.Items, []string{"health", "metrics"}) {
		t.Errorf("capabilities = %+v", got)
	}
	none := serviceDetailOf(t, detailFleet(contract.Owner{}, []*ContractRevision{{Key: "svc@1"}}, nil)).Capabilities
	if b, _ := json.Marshal(none.Items); string(b) != "[]" {
		t.Errorf("no capabilities = %s, want []", b)
	}
}

func TestServiceDetailSources(t *testing.T) {
	q := detailFleet(contract.Owner{},
		[]*ContractRevision{{Key: "svc@1", Source: "oci", Sources: []string{"cache", "oci"}}},
		[]*TargetRecord{{Key: "t1", Name: "t1", Source: "k8s", Sources: []string{"k8s", "ghost"}}})
	q.snap.Sources = []SourceState{
		{ID: "k8s", Kind: "kubernetes", Status: SourceAvailable},
		{ID: "oci", Kind: "oci", Status: SourceStale},
		{ID: "cache", Kind: "cache", Status: SourceAvailable},
	}
	got := serviceDetailOf(t, q).Sources
	var keys, statuses []string
	for _, r := range got.Items {
		if r.Kind != KindSource {
			t.Errorf("source ref kind = %q", r.Kind)
		}
		keys, statuses = append(keys, r.Key), append(statuses, r.Status)
	}
	if !slices.Equal(keys, []string{"cache", "ghost", "k8s", "oci"}) || !slices.Equal(statuses, []string{"available", "", "available", "stale"}) {
		t.Errorf("sources = %v %v", keys, statuses)
	}
}

// TestServiceDetailUnresolvedDependencies: external and unresolved declared
// dependencies are listed once each, and the three lists add up to the declared
// count, also when a provider is hidden by permissions, declared under two names
// or only observed.
func TestServiceDetailUnresolvedDependencies(t *testing.T) {
	a, b, c := ServiceKey("a"), ServiceKey("b"), ServiceKey("c")
	dep := func(rev, to string, mut func(*Relationship)) Relationship {
		r := Relationship{FromService: a, FromRevision: RevisionKey(rev), To: to, RequestedRef: to, Type: RelationshipDependency, Provenance: ProvenanceDeclared}
		mut(&r)
		return r
	}
	ext := func(r *Relationship) { r.External, r.Version = true, "15" }
	snap := &FleetSnapshot{
		Services: map[ServiceKey]*ServiceRecord{
			a: {Key: a, Name: "a", Revisions: []RevisionKey{"a@1", "a@2"}, Targets: []TargetKey{"ta"}},
			b: {Key: b, Name: "b", Targets: []TargetKey{"tb"}},
			c: {Key: c, Name: "c", Targets: []TargetKey{"tc"}},
		},
		Revisions: map[RevisionKey]*ContractRevision{
			"a@1": {Key: "a@1", Service: "a", ServiceKey: a, Version: "1.0.0"},
			"a@2": {Key: "a@2", Service: "a", ServiceKey: a, Version: "2.0.0"},
		},
		Targets: map[TargetKey]*TargetRecord{
			"ta": {Key: "ta", Name: "a", Kind: SourceKindKubernetes, Scope: "shop", ServiceKey: a},
			"tb": {Key: "tb", Name: "b", Kind: SourceKindKubernetes, Scope: "secret", ServiceKey: b},
			"tc": {Key: "tc", Name: "c", Kind: SourceKindKubernetes, Scope: "secret", ServiceKey: c},
		},
		Relationships: []Relationship{
			// The first record carries neither flag: External is OR-ed and the first
			// non-empty Version is kept.
			dep("a@1", "oci://reg/db", func(*Relationship) {}),
			dep("a@2", "oci://reg/db", ext),
			dep("a@1", "oci://reg/missing", func(*Relationship) {}),
			dep("a@1", "oci://reg/b", func(r *Relationship) { r.ToService, r.Resolved = b, true }),
			// b again under another name: still one dependency, hidden or not.
			dep("a@2", "oci://reg/b-renamed", func(r *Relationship) { r.ToService, r.Resolved = b, true }),
			// c is only called at runtime: never a declared or hidden dependency.
			{FromService: a, ToService: c, To: "c", Type: RelationshipDependency, Provenance: ProvenanceObserved},
		},
	}
	check := func(name string, q *Query, hidden int) {
		t.Helper()
		d, err := q.EntityDetail(KindService, "a")
		if err != nil {
			t.Fatal(err)
		}
		s := d.Service
		u := s.UnresolvedDependencies.Items
		if len(u) != 2 || u[0].Ref != "oci://reg/db" || !u[0].External || u[0].Version != "15" || u[1].Ref != "oci://reg/missing" || u[1].External || u[0].From.Key != "a" {
			t.Errorf("%s: unresolved = %+v", name, u)
		}
		if s.HiddenDependencies != hidden || s.Dependencies.Total+s.UnresolvedDependencies.Total+s.HiddenDependencies != s.Summary.DeclaredDependencies || s.Summary.DeclaredDependencies != 3 {
			t.Errorf("%s: %d + %d + %d != %d", name, s.Dependencies.Total, s.UnresolvedDependencies.Total, s.HiddenDependencies, s.Summary.DeclaredDependencies)
		}
	}
	q := NewQuery(snap)
	check("visible", q, 0)
	check("hidden", q.Restrict(func(ns string) bool { return ns != "secret" }), 1)
}

func TestRevisionEntityRefFetchedAt(t *testing.T) {
	at := fixedNow()
	ref := revisionEntityRef(&ContractRevision{Key: "svc@1", FetchedAt: &at})
	if ref.FetchedAt == nil || !ref.FetchedAt.Equal(at) || ref.FetchedAt == &at {
		t.Errorf("fetchedAt = %v, want a copy of %v", ref.FetchedAt, at)
	}
	if revisionEntityRef(&ContractRevision{Key: "svc@1"}).FetchedAt != nil {
		t.Error("no fetch time stays nil")
	}
}

func TestServiceRelationshipsNeighborhoodError(t *testing.T) {
	self := EntityRef{Kind: KindService, Key: "svc", Label: "svc"}
	rels, lims := serviceRelationships(nil, errors.New("boom"), self)
	if rels.Count != 0 || rels.Total != nil || rels.Items == nil {
		t.Errorf("relationships = %+v, want an empty preview of unknown total", rels)
	}
	want := AttributedLimitation{Entity: self, Limitation: Limitation{Code: LimitationNeighborhoodUnavailable, Message: "The relationships of this service could not be computed: boom"}}
	if len(lims) != 1 || lims[0] != want {
		t.Fatalf("limitations = %+v", lims)
	}
	var targets []*TargetRecord
	for i := range 250 {
		targets = append(targets, &TargetRecord{Key: TargetKey(fmt.Sprint("t", i)), Limitations: []Limitation{{Code: "X"}}})
	}
	got := serviceLimitations(lims, targets)
	if got.Total != 251 || got.Count != MaxDetailPreview || !got.Truncated || got.Items[0] != want {
		t.Errorf("limitations preview = total %d count %d first %+v", got.Total, got.Count, got.Items[0])
	}
}

func TestRevisionEdgesConfigRef(t *testing.T) {
	q := configRefFleet(t)
	d, err := q.EntityDetail(KindRevision, revKeyOf(t, q, "billing"))
	if err != nil {
		t.Fatal(err)
	}
	items := d.Revision.Dependencies.Items
	if len(items) != 2 || items[0].Relation != RelationConfigurationReference || items[1].Relation != RelationDependency {
		t.Fatalf("revision edges = %+v", items)
	}
	if e := items[0]; !strings.HasSuffix(e.ID, "|configuration_reference") || e.ObservationScope != "" || e.ServiceCorroboration != "" {
		t.Errorf("configuration reference edge = %+v", e)
	}
	if e := items[1]; e.ObservationScope != ObservationScopeService {
		t.Errorf("dependency edge lost its observation scope: %+v", e)
	} // Impact reads a consumer's environment files through BundleFS.
	if q.snap.Revisions[RevisionKey(revKeyOf(t, q, "billing"))].BundleFS() == nil || (&ContractRevision{}).BundleFS() != nil {
		t.Error("BundleFS must return the bundle's files, and nil without a bundle")
	}
}

func TestOfferingProviderLifecycle(t *testing.T) {
	q := lifecycleFleet(t)
	d, err := q.EntityDetail(KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	p := d.Offering.Providers.Items[0]
	cur := q.snap.Revisions[RevisionKey(p.CurrentRevision.Key)]
	if p.Lifecycle == nil || *p.Lifecycle != *cur.Contract.Service.Lifecycle {
		t.Errorf("provider lifecycle = %+v, current %s", p.Lifecycle, cur.Version)
	}
	// app has two revisions: the row keeps what the newest one reaches.
	u := d.Offering.Consumers.Items[0]
	if u.Consumer.Label != "app" || u.ProviderLifecycle == nil || *u.ProviderLifecycle != *newLifecycle() {
		t.Errorf("consumer row = %+v", u)
	}
}

func TestServiceDetailLifecycle(t *testing.T) {
	q := lifecycleFleet(t)
	d, err := q.EntityDetail(KindService, "pg")
	if err != nil {
		t.Fatal(err)
	}
	if l := d.Service.Lifecycle; l == nil || *l != *newLifecycle() {
		t.Errorf("service lifecycle = %+v", l)
	}
	if d, _ := q.EntityDetail(KindService, "redis"); d.Service.Lifecycle != nil {
		t.Errorf("redis declares no lifecycle, got %+v", d.Service.Lifecycle)
	}
	for k, r := range q.snap.Revisions {
		if r.Service == "pg" && r.Version == "1.0.0" {
			rd, err := q.EntityDetail(KindRevision, string(k))
			if err != nil {
				t.Fatal(err)
			}
			if l := rd.Revision.Lifecycle; l == nil || *l != *oldLifecycle() {
				t.Errorf("revision lifecycle = %+v", l)
			}
		}
	}
}

// A hidden service consuming two providers of an offering counts once; an
// observed caller and a hidden service that only references a provider's
// policy never count.
func TestOfferingHiddenConsumers(t *testing.T) {
	both := svc24("both", "1.0.0", "")
	both.Dependencies = []contract.Dependency{{Name: "pa", Required: true}, {Name: "pb", Required: true}}
	pol := svc24("pol", "1.0.0", "")
	pol.Policies = []contract.Policy{{Name: "guard", Ref: "oci://ghcr.io/acme/pa@" + testDigest("ghcr.io/acme", "pa", "1.0.0")}}
	tgt := func(ns, name string) RawTarget {
		return RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: name, Service: name, Domain: "ghcr.io/acme", Digest: testDigest("ghcr.io/acme", name, "1.0.0")}
	}
	q := buildDomainSnap(t, SourceKindKubernetes, &Collection{
		Revisions: []RawRevision{
			ociRev("ghcr.io/acme", svc24("pa", "1.0.0", "db"), nil), ociRev("ghcr.io/acme", svc24("pb", "1.0.0", "db"), nil),
			ociRev("ghcr.io/acme", both, nil), ociRev("ghcr.io/acme", pol, nil),
		},
		Targets:  []RawTarget{tgt("shop", "pa"), tgt("shop", "pb"), tgt("hush", "both"), tgt("hush", "pol")},
		Observed: []ObservedEdge{{From: "k8s:hush/Deployment/x", To: "pa", Count: 1}, {From: "somecaller", To: "pa", Count: 1}},
	})
	if !slices.ContainsFunc(q.snap.Relationships, func(r Relationship) bool { return r.Type == RelationshipPolicyRef && r.Resolved }) {
		t.Fatal("pol's policy reference does not resolve")
	}
	if n := offeringData(t, q, "db").HiddenConsumers; n != 0 {
		t.Errorf("unrestricted hidden consumers = %d", n)
	}
	r := q.Restrict(func(ns string) bool { return ns == "shop" })
	if n := offeringData(t, r, "db").HiddenConsumers; n != 1 {
		t.Errorf("hidden consumers = %d, want 1", n)
	}
	if pa := r.snap.Services[NewServiceKeyDomain("ghcr.io/acme", "pa")]; pa.HiddenDependents < 3 {
		t.Errorf("pa hidden dependents = %d, want the callers counted there", pa.HiddenDependents)
	}
}

// The offering-use and dependents tallies cover the view the caller may read:
// a hidden consumer is counted in the hidden count only, never in a tally.
func TestOfferingAndDependentsTalliesFollowRestrict(t *testing.T) {
	both := svc24("both", "1.0.0", "")
	both.Dependencies = []contract.Dependency{{Name: "pa", Required: true}}
	seen := svc24("seen", "1.0.0", "")
	seen.Dependencies = []contract.Dependency{{Name: "pa", Required: true}}
	tgt := func(ns, name string) RawTarget {
		return RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: name, Service: name, Domain: "ghcr.io/acme", Digest: testDigest("ghcr.io/acme", name, "1.0.0")}
	}
	q := buildDomainSnap(t, SourceKindKubernetes, &Collection{
		Revisions: []RawRevision{ociRev("ghcr.io/acme", svc24("pa", "1.0.0", "db"), nil), ociRev("ghcr.io/acme", both, nil), ociRev("ghcr.io/acme", seen, nil)},
		Targets:   []RawTarget{tgt("shop", "pa"), tgt("hush", "both"), tgt("shop", "seen")},
	})
	r := q.Restrict(func(ns string) bool { return ns == "shop" })
	for name, view := range map[string]*Query{"unrestricted": q, "restricted": r} {
		c := offeringData(t, view, "db").Consumers
		if got := c.Range.Satisfied + c.Range.Unsatisfied + c.Range.Unknown + c.Range.None; got != c.Total {
			t.Errorf("%s: range tally sums to %d, want %d", name, got, c.Total)
		}
		d, err := view.EntityDetail(KindService, string(NewServiceKeyDomain("ghcr.io/acme", "pa")))
		if err != nil {
			t.Fatal(err)
		}
		if got := d.Service.DependentsStatus.Total(); got != d.Service.Dependents.Total {
			t.Errorf("%s: dependents tally = %d, want %d", name, got, d.Service.Dependents.Total)
		}
	}
	if got := offeringData(t, r, "db").Consumers.Total; got != 1 {
		t.Errorf("restricted consumers = %d, want only the visible one", got)
	}
	if got := q.statusTally([]ServiceKey{"missing"}); got.Other != 1 {
		t.Errorf("a key the snapshot does not hold = %+v, want Other", got)
	}
}

func TestOfferingUsesPreviewTallies(t *testing.T) {
	st := func(phases ...string) []ProvisioningStatus {
		out := make([]ProvisioningStatus, len(phases))
		for i, p := range phases {
			out[i] = ProvisioningStatus{Phase: p}
		}
		return out
	}
	ref := func(k string) EntityRef { return EntityRef{Kind: KindService, Key: k, Label: k} }
	uses := []OfferingUse{
		{Consumer: ref("d"), RangeStatus: RangeSatisfied, Provisioning: st("Failed", "Ready")},
		{Consumer: ref("a"), RangeStatus: RangeUnsatisfied, Provisioning: st("Failed")},
		{Consumer: ref("c"), RangeStatus: RangeUnknown, Provisioning: st("Requested", "Provisioning", "Deleting", "Unknown")},
		{Consumer: ref("b"), Provisioning: st("Failed")},
		{Consumer: ref("e"), Provisioning: st("Failed")},
		{Consumer: ref("a"), Via: RelationshipPolicyRef},
		{Consumer: ref("d"), RangeStatus: RangeSatisfied, Provisioning: st("Ready")},
	}
	p := offeringUsesPreview(uses)
	if want := (RangeTally{Satisfied: 2, Unsatisfied: 1, Unknown: 1, None: 3}); p.Range != want {
		t.Errorf("range = %+v, want %+v", p.Range, want)
	}
	if want := (PhaseTally{Requested: 1, Provisioning: 1, Ready: 2, Failed: 4, Deleting: 1, Unknown: 1}); p.Phases != want {
		t.Errorf("phases = %+v, want %+v", p.Phases, want)
	}
	// d is Ready twice: two requests, one consumer, as the phase filter lists it.
	if want := (PhaseTally{Requested: 1, Provisioning: 1, Ready: 1, Failed: 4, Deleting: 1, Unknown: 1}); p.PhaseConsumers != want {
		t.Errorf("phase consumers = %+v, want %+v", p.PhaseConsumers, want)
	}
	if p.Failed.Total != 4 || p.Failed.Count != MaxFailedConsumers || !p.Failed.Truncated || p.Failed.Items[0].Key != "a" || p.Failed.Items[2].Key != "d" {
		t.Errorf("failed = %+v, want the first 3 of 4 by key", p.Failed)
	}
}

// A revision knows its place in its service, its share of the service's
// targets and the sources behind it, least healthy first.
func TestRevisionPlaceAndEvidence(t *testing.T) {
	rev := func(v string) *ContractRevision {
		return &ContractRevision{Key: RevisionKey("s@" + v), ServiceKey: "s", Version: v, Source: "oci"}
	}
	r1, r2, r3 := rev("1.0.0"), rev("2.0.0"), rev("3.0.0")
	r2.Sources = []string{"oci", "files"}
	snap := &FleetSnapshot{
		Revisions: map[RevisionKey]*ContractRevision{r1.Key: r1, r2.Key: r2, r3.Key: r3, "o@1": {Key: "o@1", ServiceKey: "o", Version: "1.0.0"}},
		Targets: map[TargetKey]*TargetRecord{
			"t1": {ServiceKey: "s", ContractRevision: r2.Key, Source: "k8s", Sources: []string{"gone"}},
			"t2": {ServiceKey: "s", ContractRevision: r1.Key},
			"t3": {ServiceKey: "o"},
		},
		Sources: []SourceState{{ID: "oci", Status: SourceAvailable}, {ID: "files", Status: SourceStale}, {ID: "k8s", Status: SourceUnavailable}, {ID: "other", Status: SourceUnavailable}},
	}
	q := NewQuery(snap)
	// The newest a target runs is current, though a newer one exists.
	if p := q.revisionPlace(r2); p != (revisionPlacement{position: 2, revisions: 3, targets: 2, current: true}) {
		t.Errorf("place of 2.0.0 = %+v", p)
	}
	if p := q.revisionPlace(r3); p.position != 1 || p.current {
		t.Errorf("place of 3.0.0 = %+v, want newest and not current", p)
	}
	refs, counts := q.revisionEvidenceSources(r2)
	var ids []string
	for _, r := range refs {
		ids = append(ids, r.Key)
	}
	if want := []string{"k8s", "files", "gone", "oci"}; !slices.Equal(ids, want) {
		t.Errorf("evidence sources = %v, want %v", ids, want)
	}
	if want := (SourceCounts{Total: 4, Available: 1, Stale: 1, Unavailable: 1}); counts != want {
		t.Errorf("evidence counts = %+v, want %+v", counts, want)
	}
}
