package fleet

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
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
// EXPLICITLY, so a consumer (the legacy version-bookmark migration, reopen section 8) can
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
	d, err := q.EntityDetail(KindOwner, "team-a")
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

	if _, err := q.EntityDetail(KindOwner, "nobody"); err == nil {
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
