package fleet

import "testing"

func TestEntities_AllKindsDefault(t *testing.T) {
	q := productFleet(t)
	list, err := q.Entities(EntityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if list.Meta.SchemaVersion != ProductSchemaVersion {
		t.Errorf("schema version = %q", list.Meta.SchemaVersion)
	}
	// 5 services + 6 revisions + 5 targets + 5 owners + 4 sources.
	if list.Total != 25 {
		t.Errorf("total entities = %d, want 25", list.Total)
	}
	// Deterministic order: grouped by kind.
	for i := 1; i < len(list.Entities); i++ {
		if list.Entities[i-1].Kind == list.Entities[i].Kind && list.Entities[i-1].Key > list.Entities[i].Key {
			t.Errorf("entities not sorted within kind at %d", i)
		}
	}
	// Every entity carries the canonical identity the transport turns into an href.
	for _, e := range list.Entities {
		if e.Key == "" || e.Kind == "" {
			t.Errorf("entity has no canonical identity: %+v", e)
		}
	}
}

func TestEntities_KindFilter(t *testing.T) {
	q := productFleet(t)
	list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 5 {
		t.Errorf("services = %d, want 5", list.Total)
	}
	for _, e := range list.Entities {
		if e.Kind != KindService {
			t.Errorf("unexpected kind %q", e.Kind)
		}
	}
}

func TestEntities_TextScopeStatus(t *testing.T) {
	q := productFleet(t)

	byText, _ := q.Entities(EntityFilter{Text: "beta"})
	if byText.Total != 4 { // service beta + 2 beta revisions + beta-app target
		t.Errorf("text beta = %d, want 4", byText.Total)
	}

	byScope, _ := q.Entities(EntityFilter{Scope: "prod"})
	if byScope.Total != 5 { // only targets carry a scope
		t.Errorf("scope prod = %d, want 5", byScope.Total)
	}
	for _, e := range byScope.Entities {
		if e.Kind != KindTarget {
			t.Errorf("scope filter leaked non-target %q", e.Kind)
		}
	}

	byStatus, _ := q.Entities(EntityFilter{Status: StatusNonCompliant})
	if byStatus.Total == 0 {
		t.Fatal("expected at least the non-compliant target")
	}
	for _, e := range byStatus.Entities {
		if e.Status != StatusNonCompliant {
			t.Errorf("status filter leaked %q", e.Status)
		}
	}
}

func TestEntities_OwnerAndSource(t *testing.T) {
	q := productFleet(t)

	byOwner, _ := q.Entities(EntityFilter{Owner: "team-a"})
	// service alpha + revision alpha + 2 alpha targets + owner "team-a".
	if byOwner.Total != 5 {
		t.Errorf("owner team-a = %d, want 5", byOwner.Total)
	}
	for _, e := range byOwner.Entities {
		if e.Kind == KindSource {
			t.Error("a source has no owner and must not match an owner filter")
		}
	}

	bySource, _ := q.Entities(EntityFilter{Source: "local"})
	// 5 services + 6 revisions + 5 targets + the "local" source entity.
	if bySource.Total != 17 {
		t.Errorf("source local = %d, want 17", bySource.Total)
	}
	for _, e := range bySource.Entities {
		if e.Kind == KindOwner {
			t.Error("an owner is derived, not sourced")
		}
	}
}

func TestEntities_DomainIsolation(t *testing.T) {
	q := NewQuery(twoDomainSnap(t))
	list, err := q.Entities(EntityFilter{Domain: "east"})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total == 0 {
		t.Fatal("expected east-domain entities")
	}
	sawEastShared := false
	for _, e := range list.Entities {
		if e.Domain != "east" {
			t.Errorf("domain filter leaked %q (domain %q)", e.Key, e.Domain)
		}
		if e.Kind == KindService && e.Key == "east/shared" {
			sawEastShared = true
		}
	}
	if !sawEastShared {
		t.Error("expected the east-domain 'shared' service")
	}
}

func TestEntities_Paging(t *testing.T) {
	q := productFleet(t)

	page1, _ := q.Entities(EntityFilter{Limit: 2})
	if page1.Count != 2 || page1.Total != 25 {
		t.Errorf("page1: count=%d total=%d", page1.Count, page1.Total)
	}

	// Offset near the end truncates the final page (end > total branch).
	tail, _ := q.Entities(EntityFilter{Offset: 24, Limit: 100})
	if tail.Count != 1 {
		t.Errorf("tail count = %d, want 1", tail.Count)
	}

	// Offset past the end yields an empty page (start > total branch).
	past, _ := q.Entities(EntityFilter{Offset: 100})
	if past.Count != 0 {
		t.Errorf("past-end count = %d, want 0", past.Count)
	}

	// A limit above the cap is capped but still returns every match here.
	big, _ := q.Entities(EntityFilter{Limit: MaxEntityLimit + 100})
	if big.Count != 25 {
		t.Errorf("capped limit count = %d, want 25", big.Count)
	}
}

func TestEntities_ValidationErrors(t *testing.T) {
	q := productFleet(t)
	cases := []EntityFilter{
		{Offset: -1},
		{Limit: -1},
		{Status: "bogus"},
		{Kinds: []EntityKind{"weird"}},
		// a service scope only applies to service/revision/target kinds.
		{Kinds: []EntityKind{KindOwner}, Service: "beta"},
		{Kinds: []EntityKind{KindSource}, Service: "beta"},
	}
	for _, f := range cases {
		if _, err := q.Entities(f); err == nil {
			t.Errorf("filter %+v: expected an error", f)
		}
	}
}

// TestEntities_ServiceScope proves the canonical parent-service scope: it lists ALL
// revisions of one service (the Product Impact selector's pageable universe), scopes
// targets to their service, and matches the service itself -- never a sibling's.
func TestEntities_ServiceScope(t *testing.T) {
	q := productFleet(t)

	// beta has TWO revisions (same version, distinct content); both must be returned.
	revs, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindRevision}, Service: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if revs.Total != 2 || revs.Count != 2 {
		t.Fatalf("beta revisions = total %d count %d, want 2/2", revs.Total, revs.Count)
	}
	for _, r := range revs.Entities {
		if r.Kind != KindRevision || r.ParentService != "beta" {
			t.Errorf("scoped revision %+v is not a beta revision", r)
		}
	}

	// alpha has exactly one revision.
	alphaRevs, _ := q.Entities(EntityFilter{Kinds: []EntityKind{KindRevision}, Service: "alpha"})
	if alphaRevs.Total != 1 {
		t.Errorf("alpha revisions = %d, want 1", alphaRevs.Total)
	}

	// targets scope to their parent service (alpha has two: alpha-app + alpha-ancient).
	tgts, _ := q.Entities(EntityFilter{Kinds: []EntityKind{KindTarget}, Service: "alpha"})
	for _, tr := range tgts.Entities {
		if tr.ParentService != "alpha" {
			t.Errorf("scoped target %+v is not an alpha target", tr)
		}
	}
	if tgts.Total < 1 {
		t.Errorf("alpha targets = %d, want >= 1", tgts.Total)
	}

	// the service entity itself matches its own key, never a sibling.
	svc, _ := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Service: "beta"})
	if svc.Total != 1 || svc.Entities[0].Key != "beta" {
		t.Errorf("service scope = %+v, want exactly the beta service", svc.Entities)
	}
}
