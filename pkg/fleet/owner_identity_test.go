package fleet

import (
	"fmt"
	"sort"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// CANONICAL OWNER IDENTITY vs FREE-TEXT OWNER SEARCH.
//
// Two questions were hiding behind one owner string. "Which owner is this?" is an
// identity: /fleet/owners/team:team-a is one row of the owner inventory, the estate the
// owner page draws, the label a ranking row carries. "Which owners look like what
// I typed?" is a search: a case-insensitive substring over team, DRI and every
// contact value, and it is genuinely useful.
//
// Answering the first with the second is wrong wherever the answer is canonical.
// With owners `team-a` and `team-a-platform` in one fleet, a link taken FROM the
// team-a owner page that carries owner=team-a comes back holding services team-a
// does not own -- the page and its own drill-down disagree about the estate.
//
// So the two questions get two filters. OwnerKey is exact canonical identity;
// Owner stays the free-text search. Nothing here removes the search.

// collidingRevisions is the counterexample fixture: two owners whose canonical
// keys are a prefix of one another, each holding a service the other does not.
func collidingRevisions() []inventoryRevision {
	return []inventoryRevision{
		{service: "checkout", owner: contract.Owner{Team: "team-a"}, distinct: "1", target: "prod"},
		{service: "billing", owner: contract.Owner{Team: "team-a-platform"}, distinct: "1", target: "prod"},
	}
}

func collidingFleet(t *testing.T) *Query {
	t.Helper()
	return inventoryFleet(t, collidingRevisions()...)
}

// THE counterexample. A canonical drill-down taken from the team-a owner page must
// hold team-a's estate and nothing else, on every kind the filter applies to.
func TestOwnerKey_CanonicalDrillDownExcludesTheSubstringCollider(t *testing.T) {
	q := collidingFleet(t)
	for _, tc := range []struct {
		kind EntityKind
		want string
	}{
		{KindService, "[checkout]"},
		{KindRevision, "[checkout@sha256:rev0]"},
		{KindOwner, "[team:team-a]"},
	} {
		got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{tc.kind}, OwnerKey: "team:team-a"})
		if fmt.Sprint(got) != tc.want {
			t.Errorf("ownerKey=team-a over %s listed %v, want %s", tc.kind, got, tc.want)
		}
	}
	// A target's owner is its service's, so it narrows with the service.
	list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindTarget}, OwnerKey: "team:team-a"})
	if err != nil {
		t.Fatalf("Entities: %v", err)
	}
	for _, e := range list.Entities {
		if e.ParentService != "checkout" {
			t.Errorf("ownerKey=team-a reached a target of %s", e.ParentService)
		}
	}
	if list.Total != 1 {
		t.Errorf("ownerKey=team-a matched %d targets, want checkout's one", list.Total)
	}
}

// And the search that makes the collision possible is still there. Removing it
// would be the other way to make these two agree, and the wrong one: a reader who
// types half an owner's name is asking a real question.
func TestOwnerFilter_FuzzySearchStillDiscoversBothCollidingOwners(t *testing.T) {
	q := collidingFleet(t)
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Owner: "team-a"}); fmt.Sprint(got) != "[billing checkout]" {
		t.Errorf("owner=team-a searched up %v, want both colliding owners' services", got)
	}
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindOwner}, Owner: "TEAM-A"}); fmt.Sprint(got) != "[team:team-a team:team-a-platform]" {
		t.Errorf("owner discovery for TEAM-A listed %v, want both owners", got)
	}
	// Free-text search reaches contacts; canonical identity never does.
	c := inventoryFleet(t, inventoryRevision{service: "paged", distinct: "1", owner: contract.Owner{
		Team: "team-a", Contacts: []contract.OwnerContact{{Type: "chat", Value: "#platform-oncall"}},
	}})
	if got := serviceKeys(t, c, EntityFilter{Kinds: []EntityKind{KindService}, Owner: "oncall"}); fmt.Sprint(got) != "[paged]" {
		t.Errorf("owner=oncall searched up %v, want the service whose contact says so", got)
	}
	if got := serviceKeys(t, c, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: "#platform-oncall"}); fmt.Sprint(got) != "[]" {
		t.Errorf("ownerKey matched a contact value: %v", got)
	}
}

// The owner page itself: each canonical Owner detail is its own estate, and the
// attention preview on it is scoped the same way the estate is. An owner page whose
// backlog is wider than its own services tells a team to fix somebody else's work.
func TestOwnerDetail_EstateAndBacklogContainOnlyItsOwnCanonicalOwner(t *testing.T) {
	q := collidingFleet(t)
	for owner, service := range map[string]string{"team:team-a": "checkout", "team:team-a-platform": "billing"} {
		d, err := q.EntityDetail(KindOwner, owner)
		if err != nil {
			t.Fatalf("EntityDetail(owner, %s): %v", owner, err)
		}
		var keys []string
		for _, ref := range d.Owner.Services.Items {
			keys = append(keys, ref.Key)
		}
		if fmt.Sprint(keys) != "["+service+"]" {
			t.Errorf("%s's estate is %v, want only [%s]", owner, keys, service)
		}
		if d.Owner.Summary.Services != 1 || d.Owner.Summary.Revisions != 1 {
			t.Errorf("%s's summary counts %d services / %d revisions, want 1 / 1",
				owner, d.Owner.Summary.Services, d.Owner.Summary.Revisions)
		}
		for _, it := range d.Owner.Attention.Items {
			if it.Service != service {
				t.Errorf("%s's backlog holds an item for %s", owner, it.Service)
			}
		}
		// The preview reports the TRUE total, so a broadened total would show even
		// when the collider's items fell past the preview bound.
		exact, err := q.Attention(AttentionFilter{OwnerKey: owner})
		if err != nil {
			t.Fatalf("Attention(ownerKey=%s): %v", owner, err)
		}
		if d.Owner.Attention.Total != exact.Total {
			t.Errorf("%s's backlog totals %d, want the %d its own estate raises",
				owner, d.Owner.Attention.Total, exact.Total)
		}
	}
}

// Attention links leave the owner page carrying the canonical key. The fixture
// raises one MISSING_READINESS item per revision, so a broadened link is visible as
// a count: the collider's item must not arrive in team-a's backlog.
func TestAttentionOwnerKey_DoesNotBroadenToACollidingOwner(t *testing.T) {
	q := collidingFleet(t)
	exact, err := q.Attention(AttentionFilter{OwnerKey: "team:team-a"})
	if err != nil {
		t.Fatalf("Attention(ownerKey=team:team-a): %v", err)
	}
	if exact.Total == 0 {
		t.Fatal("the fixture raises no attention item, so this proves nothing")
	}
	for _, it := range exact.Items {
		if it.Service != "checkout" {
			t.Errorf("ownerKey=team-a's backlog holds an item for %s", it.Service)
		}
	}
	fuzzy, err := q.Attention(AttentionFilter{Owner: "team-a"})
	if err != nil {
		t.Fatalf("Attention(owner=team-a): %v", err)
	}
	if fuzzy.Total <= exact.Total {
		t.Fatalf("the fuzzy owner search matched %d items and the canonical key %d; "+
			"the fixture is no longer a collision", fuzzy.Total, exact.Total)
	}
}

// A ranking row promises a count and a destination. The destination is canonical,
// so the count it lands on is the count it displayed -- even when another owner's
// key contains this one.
func TestOwnerKeyRanking_CountsEqualTheirCanonicalDestinations(t *testing.T) {
	q := inventoryFleet(t, append(collidingRevisions(),
		inventoryRevision{service: "invoicing", owner: contract.Owner{Team: "team-a-platform"}, distinct: "1", target: "prod"},
	)...)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if len(agg.ByOwner) != 2 {
		t.Fatalf("ranking = %+v, want one row per owner", agg.ByOwner)
	}
	for _, row := range agg.ByOwner {
		got := serviceKeys(t, q, EntityFilter{
			Kinds: []EntityKind{KindService}, OwnerKey: row.Key, Ownership: OwnershipConsistent})
		if len(got) != row.Services {
			t.Errorf("%s ranks %d services but its canonical drill-down lists %d: %v",
				row.Key, row.Services, len(got), got)
		}
	}
	// The shorter key is the one a substring filter would inflate; pin it.
	for _, row := range agg.ByOwner {
		if row.Key == "team:team-a" && row.Services != 1 {
			t.Errorf("team-a ranks %d services, want its own one", row.Services)
		}
	}
}

// ONE canonical owner, however its revisions spell the rest of the block.
//
// `{team: platform, dri: alice}` and `{team: platform, dri: bob}` are different
// structured owners and the SAME canonical owner: the product routes owners by
// canonical key, so there is exactly one /fleet/owners/team:platform page and it owns both
// revisions. Classifying the service CONFLICTING would say two teams are arguing
// over it when one team is, and would keep it out of a ranking whose only row it
// belongs to. The normalization rule is therefore: the identity that partitions a
// service is the canonical owner KEY.
func TestOwnershipIdentity_SameTeamDifferentDRIIsOneCanonicalOwner(t *testing.T) {
	q := inventoryFleet(t,
		inventoryRevision{service: "ledger", distinct: "1", target: "prod",
			owner: contract.Owner{Team: "platform", DRI: "alice"}},
		inventoryRevision{service: "ledger", distinct: "2",
			owner: contract.Owner{Team: "platform", DRI: "bob"}},
	)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if want := (OwnershipTally{Consistent: 1}); agg.Ownership != want {
		t.Errorf("ownership = %+v, want %+v: one team, two DRIs, no dispute", agg.Ownership, want)
	}
	if len(agg.ByOwner) != 1 || agg.ByOwner[0].Key != "team:platform" || agg.ByOwner[0].Services != 1 {
		t.Errorf("ranking = %+v, want the one owner the product routes to", agg.ByOwner)
	}
	if got := serviceKeys(t, q, EntityFilter{
		Kinds: []EntityKind{KindService}, OwnerKey: "team:platform", Ownership: OwnershipConsistent}); fmt.Sprint(got) != "[ledger]" {
		t.Errorf("the ranking row drills down to %v, want [ledger]", got)
	}
	// deriveOwner raises OWNER_CONFLICT by the SAME rule the tally partitions by --
	// the tally's doc says so -- so a limitation here would contradict the count above.
	for _, l := range q.snap.Limitations {
		if l.Code == LimitationOwnerConflict {
			t.Errorf("one canonical owner still raised %s: %s", l.Code, l.Message)
		}
	}
	// Both revisions belong to the one owner page.
	d, err := q.EntityDetail(KindOwner, "team:platform")
	if err != nil {
		t.Fatalf("EntityDetail(owner, team:platform): %v", err)
	}
	if d.Owner.Summary.Revisions != 2 {
		t.Errorf("the platform page holds %d revisions, want both", d.Owner.Summary.Revisions)
	}
}

// The normalization is over the canonical KEY, and an owner that declares only
// contacts has none. Two such owners are two claims, exactly as structured equality
// already said -- there is no key to collapse them onto, and folding them together
// would invent an agreement nobody declared.
func TestOwnershipIdentity_KeylessOwnersAreStillComparedStructurally(t *testing.T) {
	contactsOnly := func(v string) contract.Owner {
		return contract.Owner{Contacts: []contract.OwnerContact{{Type: "chat", Value: v}}}
	}
	q := inventoryFleet(t,
		inventoryRevision{service: "twinned", owner: contactsOnly("#pager"), distinct: "1"},
		inventoryRevision{service: "twinned", owner: contactsOnly("#pager"), distinct: "2"},
		inventoryRevision{service: "split", owner: contactsOnly("#one"), distinct: "1"},
		inventoryRevision{service: "split", owner: contactsOnly("#two"), distinct: "2"},
	)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if want := (OwnershipTally{Consistent: 1, Conflicting: 1}); agg.Ownership != want {
		t.Errorf("ownership = %+v, want %+v: the same contact block twice is one claim, two are two", agg.Ownership, want)
	}
	if len(agg.ByOwner) != 0 {
		t.Errorf("a keyless owner reached the ranking: %+v", agg.ByOwner)
	}
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipConflicting}); fmt.Sprint(got) != "[split]" {
		t.Errorf("ownership=conflicting listed %v, want [split]", got)
	}
}

// Structural comparison of a keyless owner is comparison of a SET of contact
// points, so writing one of them twice is the same declaration written two ways. A
// service whose revisions spell it each way is owned by one owner; reporting it as
// disputed would put a service in the conflicting bucket over a repeated line in a
// YAML file, with no second owner anywhere to be the other side of the argument.
func TestOwnershipIdentity_ARepeatedContactPointIsNotASecondClaim(t *testing.T) {
	pager := contract.OwnerContact{Type: "chat", Value: "#pager"}
	mail := contract.OwnerContact{Type: "email", Value: "ops@acme.com"}
	q := inventoryFleet(t,
		inventoryRevision{service: "repeated", distinct: "1",
			owner: contract.Owner{Contacts: []contract.OwnerContact{pager}}},
		inventoryRevision{service: "repeated", distinct: "2",
			owner: contract.Owner{Contacts: []contract.OwnerContact{pager, pager}}},
		// The control: a genuinely extra contact point is still a second claim.
		inventoryRevision{service: "extended", distinct: "1",
			owner: contract.Owner{Contacts: []contract.OwnerContact{pager}}},
		inventoryRevision{service: "extended", distinct: "2",
			owner: contract.Owner{Contacts: []contract.OwnerContact{pager, mail}}},
	)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if want := (OwnershipTally{Consistent: 1, Conflicting: 1}); agg.Ownership != want {
		t.Errorf("ownership = %+v, want %+v: repetition is not disagreement", agg.Ownership, want)
	}
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipConflicting}); fmt.Sprint(got) != "[extended]" {
		t.Errorf("ownership=conflicting listed %v, want [extended]", got)
	}
	conflicts := 0
	for _, l := range q.snap.Limitations {
		if l.Code == LimitationOwnerConflict {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Errorf("%s raised %d times, want 1 — only `extended` disagrees", LimitationOwnerConflict, conflicts)
	}
}

// The correction narrows canonical links; it must not narrow what a conflict IS.
// Two different teams still conflict, and both still find the service.
func TestOwnershipIdentity_DifferentCanonicalOwnersStillConflict(t *testing.T) {
	q := disputedFleet(t)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if agg.Ownership.Conflicting != 1 {
		t.Errorf("ownership = %+v, want the disputed service still conflicting", agg.Ownership)
	}
	for _, owner := range []string{"team:team-x", "team:team-y"} {
		got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: owner})
		if !containsStr(got, "disputed") {
			t.Errorf("ownerKey=%s lists %v, and a canonical claimant must still find what it declares", owner, got)
		}
	}
}

// OwnerKey is validated like every other filter: it applies to the kinds that have
// an owner, and asking a source for one is a query error rather than an empty page.
func TestOwnerKey_IsRejectedOnKindsThatHaveNoOwner(t *testing.T) {
	q := collidingFleet(t)
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, OwnerKey: "team:team-a"}); err == nil {
		t.Fatal("ownerKey on sources should be an InvalidQueryError")
	}
	for _, k := range []EntityKind{KindService, KindRevision, KindTarget, KindOwner} {
		if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{k}, OwnerKey: "team:team-a"}); err != nil {
			t.Errorf("ownerKey on %s: %v", k, err)
		}
	}
	// Unrestricted, the filter still applies to every candidate: a source is not owned
	// by anyone, so it drops out of an owner-scoped list rather than riding along in it.
	all, err := q.Entities(EntityFilter{OwnerKey: "team:team-a", Limit: 100})
	if err != nil {
		t.Fatalf("unrestricted ownerKey: %v", err)
	}
	for _, e := range all.Entities {
		if e.Kind == KindSource {
			t.Errorf("ownerKey listed source %s, and a source has no owner to be", e.Key)
		}
	}
	// The two filters compose: an exact owner AND a search over the same population.
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: "team:team-a", Owner: "team-a"}); fmt.Sprint(got) != "[checkout]" {
		t.Errorf("ownerKey + owner listed %v, want the intersection [checkout]", got)
	}
}

// None of the canonical answers may depend on the order the revisions arrived in.
func TestOwnerKeyAnswers_AreIndependentOfDeclarationOrder(t *testing.T) {
	forward := append(collidingRevisions(),
		inventoryRevision{service: "ledger", distinct: "1", owner: contract.Owner{Team: "platform", DRI: "alice"}},
		inventoryRevision{service: "ledger", distinct: "2", owner: contract.Owner{Team: "platform", DRI: "bob"}},
	)
	reversed := make([]inventoryRevision, 0, len(forward))
	for i := len(forward) - 1; i >= 0; i-- {
		reversed = append(reversed, forward[i])
	}
	ask := func(q *Query) string {
		var b []string
		for _, key := range []string{"team:team-a", "team:team-a-platform", "team:platform"} {
			b = append(b, fmt.Sprint(serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: key})))
			d, err := q.EntityDetail(KindOwner, key)
			if err != nil {
				t.Fatalf("EntityDetail(owner, %s): %v", key, err)
			}
			b = append(b, fmt.Sprintf("%+v", d.Owner.Summary))
			att, err := q.Attention(AttentionFilter{OwnerKey: key})
			if err != nil {
				t.Fatalf("Attention(ownerKey=%s): %v", key, err)
			}
			b = append(b, fmt.Sprint(att.Total))
		}
		agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
		b = append(b, fmt.Sprintf("%+v %+v", agg.Ownership, agg.ByOwner))
		var lims []string
		for _, l := range q.snap.Limitations {
			lims = append(lims, string(l.Code))
		}
		sort.Strings(lims)
		return fmt.Sprint(b, lims)
	}
	if a, z := ask(inventoryFleet(t, forward...)), ask(inventoryFleet(t, reversed...)); a != z {
		t.Errorf("declaration order changed the answers:\n forward: %s\nreversed: %s", a, z)
	}
}
