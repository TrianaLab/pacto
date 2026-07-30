package fleet

import (
	"strings"
	"testing"

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
	if d.Summary["dependencies"] != 2 || d.Summary["deployments"] != 2 {
		t.Errorf("summary = %+v", d.Summary)
	}
	// alpha->leaf declared+observed and beta->alpha observed shadow.
	if len(d.Relationships) != 2 {
		t.Errorf("relationships = %d, want 2", len(d.Relationships))
	}
	if len(d.Evidence) != 2 {
		t.Errorf("evidence = %d, want 2 (alpha-app + alpha-ancient)", len(d.Evidence))
	}
	if d.Ownership == nil || d.Ownership.Owner != "team-a" || d.Ownership.Ref == nil {
		t.Errorf("ownership = %+v", d.Ownership)
	}
	rels := map[string]bool{}
	for _, l := range d.Links {
		rels[l.Rel] = l.Route != ""
	}
	for _, want := range []string{"graph", "compare", "impact"} {
		if !rels[want] {
			t.Errorf("missing link %q", want)
		}
	}
	if len(d.AvailableActions) != 3 {
		t.Errorf("actions = %v", d.AvailableActions)
	}
}

func TestEntityDetail_ServiceFindingsAndErrors(t *testing.T) {
	q := productFleet(t)
	beta, err := q.EntityDetail(KindService, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if len(beta.Findings) != 1 {
		t.Errorf("beta findings = %d, want 1", len(beta.Findings))
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
	if d.Entity.Kind != KindRevision {
		t.Errorf("kind = %q", d.Entity.Kind)
	}
	if tools, ok := d.Sections["tools"].([]ToolSummary); !ok || len(tools) == 0 {
		t.Errorf("expected tools section, got %T", d.Sections["tools"])
	}
	if inferred, ok := d.Sections["inferredTargets"].([]EntityRef); !ok || len(inferred) != 1 {
		t.Errorf("expected 1 inferred target, got %v", d.Sections["inferredTargets"])
	}

	// alpha revision: one declared edge (leaf), exact targets, valid.
	alphaRev := revKeyOf(t, q, "alpha")
	da, _ := q.EntityDetail(KindRevision, alphaRev)
	if len(da.Relationships) != 1 {
		t.Errorf("alpha revision edges = %d, want 1", len(da.Relationships))
	}
	if exact, ok := da.Sections["exactTargets"].([]EntityRef); !ok || len(exact) != 2 {
		t.Errorf("expected 2 exact targets, got %v", da.Sections["exactTargets"])
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

func TestEntityDetail_Target(t *testing.T) {
	q := productFleet(t)

	alphaApp := string(NewTargetKey("prod", "k8s", "alpha-app"))
	d, err := q.EntityDetail(KindTarget, alphaApp)
	if err != nil {
		t.Fatal(err)
	}
	if d.Sections["coverage"] == nil || d.Sections["observedRuntime"] == nil {
		t.Errorf("expected coverage + observedRuntime sections: %+v", d.Sections)
	}
	if len(d.Evidence) != 1 {
		t.Errorf("evidence = %d, want 1", len(d.Evidence))
	}
	linkRels := map[string]bool{}
	for _, l := range d.Links {
		linkRels[l.Rel] = true
	}
	for _, want := range []string{"service", "revision", "source", "graph"} {
		if !linkRels[want] {
			t.Errorf("alpha-app missing link %q", want)
		}
	}

	// beta-app: ambiguous (no revision link), has a finding, no evidence coverage.
	betaApp := string(NewTargetKey("prod", "k8s", "beta-app"))
	db, _ := q.EntityDetail(KindTarget, betaApp)
	if len(db.Findings) != 1 {
		t.Errorf("beta-app findings = %d, want 1", len(db.Findings))
	}
	for _, l := range db.Links {
		if l.Rel == "revision" {
			t.Error("ambiguous target must not link to a revision")
		}
	}
	if !hasLimitation(db.Limitations, LimitationRevisionAmbiguous) {
		t.Errorf("beta-app must carry the ambiguous limitation: %+v", db.Limitations)
	}

	// solo-app: unresolved, no evidence.
	soloApp := string(NewTargetKey("prod", "k8s", "solo-app"))
	ds, _ := q.EntityDetail(KindTarget, soloApp)
	if len(ds.Evidence) != 0 {
		t.Errorf("solo-app evidence = %d, want 0", len(ds.Evidence))
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
	if d.Entity.Kind != KindOwner {
		t.Errorf("kind = %q", d.Entity.Kind)
	}
	services, ok := d.Sections["services"].([]EntityRef)
	if !ok || len(services) != 1 || services[0].Key != "alpha" {
		t.Errorf("owner services = %v", d.Sections["services"])
	}
	if revs, ok := d.Sections["revisions"].([]EntityRef); !ok || len(revs) != 1 {
		t.Errorf("owner revisions = %v", d.Sections["revisions"])
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
	if local.Status != string(SourceAvailable) {
		t.Errorf("local status = %q", local.Status)
	}
	if local.Summary["lastSuccessfulSync"] == nil {
		t.Error("available source must report last successful sync")
	}
	if ents, ok := local.Sections["entities"].([]EntityRef); !ok || len(ents) == 0 {
		t.Error("local source must list contributed entities")
	}

	down, _ := q.EntityDetail(KindSource, "down")
	if down.Status != string(SourceUnavailable) {
		t.Errorf("down status = %q", down.Status)
	}
	if down.Sections["error"] == nil {
		t.Error("unavailable source must report an error")
	}
	if down.Summary["lastSuccessfulSync"] != nil {
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

	// The api revision declares db + cache, so its edge list has two entries and
	// the edge-ordering comparator runs.
	apiRev := revKeyOf(t, q, "api")
	d, err := q.EntityDetail(KindRevision, apiRev)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Relationships) != 2 {
		t.Errorf("api revision edges = %d, want 2", len(d.Relationships))
	}

	// The api service has a declared dependent (web), so serviceKeyRefs iterates.
	ds, err := q.EntityDetail(KindService, "api")
	if err != nil {
		t.Fatal(err)
	}
	deps, ok := ds.Sections["dependents"].([]EntityRef)
	if !ok || len(deps) != 1 || deps[0].Key != "web" {
		t.Errorf("api dependents = %v", ds.Sections["dependents"])
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
	if len(info.Conflicts) != 1 || !strings.Contains(info.Conflicts[0], "team-y") {
		t.Errorf("conflicts = %v", info.Conflicts)
	}

	empty := serviceOwnership(&ServiceRecord{Key: "svc2"}, nil)
	if empty.Owner != "" || empty.Ref != nil {
		t.Errorf("ownerless service must have no owner ref: %+v", empty)
	}
}
