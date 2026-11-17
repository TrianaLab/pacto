package fleet

import "testing"

func unresolvedRel(from ServiceKey, rev RevisionKey, typ, ref string) Relationship {
	return Relationship{FromService: from, FromRevision: rev, Type: typ, Provenance: ProvenanceDeclared, RequestedRef: ref}
}

// A reference matches a provider by the repository of one of its revisions or
// by a last path segment that is its name, with or without -pacto.
func TestUnresolvedRefCountMatchesRepoOrName(t *testing.T) {
	pg := NewServiceKey("postgresql")
	snap := &FleetSnapshot{
		Revisions: map[RevisionKey]*ContractRevision{"pg": {ServiceKey: pg, RequestedRef: "oci://reg.example/db/pg-images:16"}},
		Relationships: []Relationship{
			unresolvedRel("a", "a1", RelationshipConfigRef, "oci://reg.example/db/pg-images"),
			unresolvedRel("b", "b1", RelationshipConfigRef, "oci://elsewhere/postgresql-pacto:1.0.0"),
			unresolvedRel("c", "c1", RelationshipConfigRef, "oci://x/redis:7"),
		},
	}
	if n := UnresolvedRefCount(snap, []ServiceKey{pg}, RelationshipConfigRef); n != 2 {
		t.Errorf("count = %d, want the repository and the name match", n)
	}
	if n := UnresolvedRefCount(&FleetSnapshot{Relationships: snap.Relationships[2:]}, []ServiceKey{pg}, RelationshipConfigRef); n != 0 {
		t.Errorf("a ref to another name counted %d", n)
	}
	if n := UnresolvedRefCount(snap, []ServiceKey{pg}, RelationshipPolicyRef); n != 0 {
		t.Errorf("a type not asked for counted %d", n)
	}
}

// One reference that may name two providers is one unresolved use.
func TestUnresolvedRefCountCountsOnce(t *testing.T) {
	a, b := NewServiceKeyDomain("ghcr.io/one", "db"), NewServiceKeyDomain("ghcr.io/two", "db")
	rel := unresolvedRel("web", "w1", RelationshipDependency, "oci://somewhere/db")
	rel.NameFromRef = true
	snap := &FleetSnapshot{Relationships: []Relationship{rel}}
	if n := UnresolvedRefCount(snap, []ServiceKey{a, b}, RelationshipDependency); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
	// A hidden consumer's derived-name dependency counts too.
	if n := UnresolvedRefCount(&FleetSnapshot{hiddenUnresolvedRefs: appendHiddenUnresolved(nil, rel)}, []ServiceKey{a}, RelationshipDependency); n != 1 {
		t.Errorf("hidden: count = %d, want 1", n)
	}
}

// A dependency that declares its name resolves by that name: unresolved, it
// names another service, never the provider its ref's last segment matches.
func TestUnresolvedRefCountDeclaredDependencyNameIsNoMatch(t *testing.T) {
	snap := &FleetSnapshot{Relationships: []Relationship{unresolvedRel("web", "w1", RelationshipDependency, "oci://somewhere/db")}}
	if n := UnresolvedRefCount(snap, []ServiceKey{NewServiceKey("db")}, RelationshipDependency); n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

// A consumer counts once per reference whatever its revision count, a hidden
// consumer too; two hidden consumers count two.
func TestUnresolvedRefCountOncePerConsumer(t *testing.T) {
	pg := NewServiceKey("postgresql")
	var rels []Relationship
	for _, rev := range []RevisionKey{"v1", "v2", "v3"} {
		rels = append(rels, unresolvedRel("web", rev, RelationshipConfigRef, "oci://x/postgresql:16"))
	}
	if n := UnresolvedRefCount(&FleetSnapshot{Relationships: rels}, []ServiceKey{pg}, RelationshipConfigRef); n != 1 {
		t.Errorf("visible: count = %d, want 1", n)
	}
	var hidden []hiddenUnresolvedRef
	for _, rev := range []RevisionKey{"v1", "v2", "v3"} {
		hidden = appendHiddenUnresolved(hidden, unresolvedRel("secret", rev, RelationshipConfigRef, "oci://x/postgresql:16"))
	}
	snap := &FleetSnapshot{hiddenUnresolvedRefs: hidden}
	if n := UnresolvedRefCount(snap, []ServiceKey{pg}, RelationshipConfigRef); n != 1 {
		t.Errorf("one hidden consumer: count = %d, want 1", n)
	}
	snap.hiddenUnresolvedRefs = appendHiddenUnresolved(snap.hiddenUnresolvedRefs, unresolvedRel("other", "o1", RelationshipConfigRef, "oci://x/postgresql:16"))
	if n := UnresolvedRefCount(snap, []ServiceKey{pg}, RelationshipConfigRef); n != 2 {
		t.Errorf("two hidden consumers: count = %d, want 2", n)
	}
}

// Resolved, observed, external and other-typed edges are never unresolved
// uses, and the hidden list keeps none of them.
func TestUnresolvedRefCountIgnoresResolvedAndObserved(t *testing.T) {
	pg := NewServiceKey("postgresql")
	resolved := unresolvedRel("a", "a1", RelationshipConfigRef, "oci://x/postgresql")
	resolved.Resolved = true
	observed := unresolvedRel("b", "b1", RelationshipDependency, "oci://x/postgresql")
	observed.Provenance = ProvenanceObserved
	external := unresolvedRel("c", "c1", RelationshipDependency, "oci://x/postgresql")
	external.External = true
	other := unresolvedRel("d", "d1", "calls", "oci://x/postgresql")
	rels := []Relationship{resolved, observed, external, other}
	var hidden []hiddenUnresolvedRef
	for _, r := range rels {
		hidden = appendHiddenUnresolved(hidden, r)
	}
	if len(hidden) != 0 {
		t.Errorf("hidden kept %+v", hidden)
	}
	if n := UnresolvedRefCount(&FleetSnapshot{Relationships: rels}, []ServiceKey{pg}, RelationshipConfigRef, RelationshipDependency, "calls"); n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}
