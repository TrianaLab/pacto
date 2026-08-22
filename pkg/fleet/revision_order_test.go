package fleet

import "testing"

// revRec builds a bare ContractRevision for sibling-ordering tests: the content
// string becomes both the RevisionKey's content id (so distinct digests produce
// distinct keys) and the recorded Digest.
func revRec(svcKey ServiceKey, service, version, content string) *ContractRevision {
	return &ContractRevision{
		Key:        NewRevisionKey(svcKey, content),
		ServiceKey: svcKey,
		Service:    service,
		Version:    version,
		Digest:     content,
	}
}

// siblingQuery builds a Query over a snapshot holding exactly the given revisions.
func siblingQuery(revs ...*ContractRevision) *Query {
	m := map[RevisionKey]*ContractRevision{}
	for _, r := range revs {
		m[r.Key] = r
	}
	return NewQuery(&FleetSnapshot{Revisions: m})
}

func keyOrNil(r *EntityRef) string {
	if r == nil {
		return ""
	}
	return r.Key
}

// TestSiblingRevisions_SemverChronology proves 1.9.0 < 1.10.0 < 2.0.0 (never the
// naive lexical/dotted-integer order) and that the ends are nil.
func TestSiblingRevisions_SemverChronology(t *testing.T) {
	sk := NewServiceKey("web")
	r19 := revRec(sk, "web", "1.9.0", "sha256:a")
	r110 := revRec(sk, "web", "1.10.0", "sha256:b")
	r20 := revRec(sk, "web", "2.0.0", "sha256:c")
	q := siblingQuery(r19, r110, r20)

	prev, next := q.siblingRevisions(r110)
	if keyOrNil(prev) != string(r19.Key) || keyOrNil(next) != string(r20.Key) {
		t.Errorf("1.10.0 siblings = prev %q next %q, want prev %q next %q",
			keyOrNil(prev), keyOrNil(next), r19.Key, r20.Key)
	}
	// Oldest: no previous. Newest: no next.
	if prev, next = q.siblingRevisions(r19); prev != nil || keyOrNil(next) != string(r110.Key) {
		t.Errorf("1.9.0 siblings = prev %q next %q, want prev nil next %q", keyOrNil(prev), keyOrNil(next), r110.Key)
	}
	if prev, next = q.siblingRevisions(r20); keyOrNil(prev) != string(r110.Key) || next != nil {
		t.Errorf("2.0.0 siblings = prev %q next %q, want prev %q next nil", keyOrNil(prev), keyOrNil(next), r110.Key)
	}
}

// TestSiblingRevisions_Prerelease proves a prerelease sorts before its release.
func TestSiblingRevisions_Prerelease(t *testing.T) {
	sk := NewServiceKey("api")
	alpha := revRec(sk, "api", "1.0.0-alpha", "sha256:1")
	beta := revRec(sk, "api", "1.0.0-beta", "sha256:2")
	rel := revRec(sk, "api", "1.0.0", "sha256:3")
	q := siblingQuery(alpha, beta, rel)

	prev, next := q.siblingRevisions(beta)
	if keyOrNil(prev) != string(alpha.Key) || keyOrNil(next) != string(rel.Key) {
		t.Errorf("1.0.0-beta siblings = prev %q next %q, want prev %q next %q",
			keyOrNil(prev), keyOrNil(next), alpha.Key, rel.Key)
	}
}

// TestSiblingRevisions_DigestDoesNotDriveOrder proves that content digests never
// reorder distinct versions: 1.0.0 has a lexically LATER digest than 2.0.0, so a
// key-lexical sort would (wrongly) place 2.0.0 first. Canonical order keeps
// 1.0.0 -> 2.0.0.
func TestSiblingRevisions_DigestDoesNotDriveOrder(t *testing.T) {
	sk := NewServiceKey("db")
	// digest of 1.0.0 sorts AFTER digest of 2.0.0 lexically.
	r1 := revRec(sk, "db", "1.0.0", "sha256:zzz")
	r2 := revRec(sk, "db", "2.0.0", "sha256:aaa")
	q := siblingQuery(r1, r2)

	if prev, next := q.siblingRevisions(r2); keyOrNil(prev) != string(r1.Key) || next != nil {
		t.Errorf("2.0.0 siblings = prev %q next %q, want prev %q (1.0.0) next nil", keyOrNil(prev), keyOrNil(next), r1.Key)
	}
	if prev, next := q.siblingRevisions(r1); prev != nil || keyOrNil(next) != string(r2.Key) {
		t.Errorf("1.0.0 siblings = prev %q next %q, want prev nil next %q (2.0.0)", keyOrNil(prev), keyOrNil(next), r2.Key)
	}
}

// TestSiblingRevisions_Deterministic proves the order is stable regardless of the
// map/source permutation: two snapshots built with a different insertion order
// yield identical siblings (the total order never depends on iteration order).
func TestSiblingRevisions_Deterministic(t *testing.T) {
	sk := NewServiceKey("svc")
	a := revRec(sk, "svc", "1.0.0", "sha256:a")
	b := revRec(sk, "svc", "1.5.0", "sha256:b")
	c := revRec(sk, "svc", "2.0.0", "sha256:c")
	q1 := siblingQuery(a, b, c)
	q2 := siblingQuery(c, a, b) // different construction order
	for _, r := range []*ContractRevision{a, b, c} {
		p1, n1 := q1.siblingRevisions(r)
		p2, n2 := q2.siblingRevisions(r)
		if keyOrNil(p1) != keyOrNil(p2) || keyOrNil(n1) != keyOrNil(n2) {
			t.Errorf("%s siblings differ by construction order: q1(prev %q next %q) q2(prev %q next %q)",
				r.Version, keyOrNil(p1), keyOrNil(n1), keyOrNil(p2), keyOrNil(n2))
		}
	}
	// And the middle version is between the two valid neighbors.
	if p, n := q1.siblingRevisions(b); keyOrNil(p) != string(a.Key) || keyOrNil(n) != string(c.Key) {
		t.Errorf("1.5.0 siblings = prev %q next %q, want %q/%q", keyOrNil(p), keyOrNil(n), a.Key, c.Key)
	}
}

// TestSiblingRevisions_NonSemverDeterministic proves non-semver / missing versions
// sort AFTER all semver revisions, ordered by their immutable key (deterministic).
func TestSiblingRevisions_NonSemverDeterministic(t *testing.T) {
	sk := NewServiceKey("mix")
	rel := revRec(sk, "mix", "1.0.0", "sha256:rel")
	// two non-semver revisions; keys tie-break by content id ("aaa" < "bbb").
	naA := revRec(sk, "mix", "latest", "sha256:aaa")
	naB := revRec(sk, "mix", "", "sha256:bbb")
	q := siblingQuery(rel, naA, naB)

	// Semver revision is first (no previous); its next is the lower-keyed non-semver.
	if prev, next := q.siblingRevisions(rel); prev != nil || keyOrNil(next) != string(naA.Key) {
		t.Errorf("1.0.0 siblings = prev %q next %q, want prev nil next %q", keyOrNil(prev), keyOrNil(next), naA.Key)
	}
	// Between the two non-semver: naA has prev=rel, next=naB; naB has prev=naA, next=nil.
	if prev, next := q.siblingRevisions(naA); keyOrNil(prev) != string(rel.Key) || keyOrNil(next) != string(naB.Key) {
		t.Errorf("latest siblings = prev %q next %q, want %q/%q", keyOrNil(prev), keyOrNil(next), rel.Key, naB.Key)
	}
	if prev, next := q.siblingRevisions(naB); keyOrNil(prev) != string(naA.Key) || next != nil {
		t.Errorf("(missing) siblings = prev %q next %q, want prev %q next nil", keyOrNil(prev), keyOrNil(next), naA.Key)
	}
}

// TestSiblingRevisions_OnlySameService proves revisions of a different service are
// never siblings, and a lone revision has no neighbors.
func TestSiblingRevisions_OnlySameService(t *testing.T) {
	web := NewServiceKey("web")
	api := NewServiceKey("api")
	w1 := revRec(web, "web", "1.0.0", "sha256:w1")
	a1 := revRec(api, "api", "1.0.0", "sha256:a1")
	a2 := revRec(api, "api", "2.0.0", "sha256:a2")
	q := siblingQuery(w1, a1, a2)

	if prev, next := q.siblingRevisions(w1); prev != nil || next != nil {
		t.Errorf("lone web revision has siblings: prev %q next %q", keyOrNil(prev), keyOrNil(next))
	}
	if prev, next := q.siblingRevisions(a1); prev != nil || keyOrNil(next) != string(a2.Key) {
		t.Errorf("api 1.0.0 siblings = prev %q next %q, want prev nil next %q (never web)", keyOrNil(prev), keyOrNil(next), a2.Key)
	}
}
