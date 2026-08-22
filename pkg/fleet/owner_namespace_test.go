package fleet

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// A DISPLAY LABEL IS NOT AN IDENTITY.
//
// The canonical owner key used to be the owner's display string: team if present,
// else DRI. That is the label a reader sees, and the contract lets two different
// owners have the same one — a team called `alice` and a person called `alice` are
// two owners with two estates, two backlogs and two pages. Routing both to one key
// merged them: one owner page holding both estates, one ranking row counting both,
// and — worst — a service whose revisions named each of them classified as
// CONSISTENTLY owned, because the two claims deduplicated into one.
//
// So identity is now namespaced ([contract.OwnerKey]): `team:alice` and
// `dri:alice`. The label stays where it belongs, on the label.

// sameLabelFleet is the counterexample fixture: two owners, one label.
func sameLabelFleet(t *testing.T) *Query {
	t.Helper()
	return inventoryFleet(t,
		inventoryRevision{service: "checkout", distinct: "1", target: "prod", owner: contract.Owner{Team: "alice"}},
		inventoryRevision{service: "billing", distinct: "1", target: "prod", owner: contract.Owner{DRI: "alice"}},
	)
}

// A: the two owners are two canonical identities everywhere the product counts
// owners, and neither one's key reaches the other's estate.
func TestOwnerNamespace_TeamAndDRIWithOneLabelAreTwoOwners(t *testing.T) {
	q := sameLabelFleet(t)
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindOwner}}); fmt.Sprint(got) != "[dri:alice team:alice]" {
		t.Fatalf("owner roster = %v, want both namespaces", got)
	}
	for key, own := range map[string]string{"team:alice": "checkout", "dri:alice": "billing"} {
		if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: key}); fmt.Sprint(got) != "["+own+"]" {
			t.Errorf("ownerKey=%s listed %v, want [%s]", key, got, own)
		}
	}
	// The un-namespaced label resolves to NEITHER. Fail closed: answering it with one
	// of them would be a coin toss shown as a fact, and with both would re-merge them.
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: "alice"}); fmt.Sprint(got) != "[]" {
		t.Errorf("the bare label resolved to %v; a raw owner name is ambiguous and must match nothing", got)
	}
	if _, err := q.EntityDetail(KindOwner, "alice"); err == nil {
		t.Error("the bare label opened an owner page; there are two owners called alice")
	}
	// The aggregate counts two owners, and each ranks its own single service.
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if agg.DistinctOwners != 2 || agg.RankedOwners != 2 || len(agg.ByOwner) != 2 {
		t.Fatalf("aggregate merged the two owners: distinct=%d ranked=%d byOwner=%+v",
			agg.DistinctOwners, agg.RankedOwners, agg.ByOwner)
	}
	for _, row := range agg.ByOwner {
		if row.Label != "alice" || row.Services != 1 {
			t.Errorf("ranking row %+v, want one service under the label alice", row)
		}
		if row.Kind != "team" && row.Kind != "dri" {
			t.Errorf("row %+v carries no namespace, so a reader cannot tell the two rows apart", row)
		}
	}
	if agg.ByOwner[0].Kind == agg.ByOwner[1].Kind {
		t.Errorf("both ranking rows claim the same namespace: %+v", agg.ByOwner)
	}
}

// B: THE counterexample. One service, two revisions, one label, two owners. The
// old identity deduplicated the claims and reported agreement.
func TestOwnerNamespace_OneLabelTwoNamespacesIsAConflict(t *testing.T) {
	q := inventoryFleet(t,
		inventoryRevision{service: "ledger", distinct: "1", target: "prod", owner: contract.Owner{Team: "alice"}},
		inventoryRevision{service: "ledger", distinct: "2", owner: contract.Owner{DRI: "alice"}},
	)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if want := (OwnershipTally{Conflicting: 1}); agg.Ownership != want {
		t.Errorf("ownership = %+v, want %+v: a team and a person are not each other", agg.Ownership, want)
	}
	if len(agg.ByOwner) != 0 || agg.UnidentifiedOwnership != 0 {
		t.Errorf("a disputed service reached the ranking: byOwner=%+v unidentified=%d",
			agg.ByOwner, agg.UnidentifiedOwnership)
	}
	// Both claimants are still present in the population, and both find the service.
	if agg.DistinctOwners != 2 {
		t.Errorf("distinctOwners = %d, want both claimants", agg.DistinctOwners)
	}
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipConflicting}); fmt.Sprint(got) != "[ledger]" {
		t.Errorf("ownership=conflicting listed %v, want [ledger]", got)
	}
	// And the limitation agrees with the tally, because both count the same set.
	var conflicts int
	for _, l := range q.snap.Limitations {
		if l.Code == LimitationOwnerConflict {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Errorf("OWNER_CONFLICT raised %d times, want 1 — the tally and the limitation must agree", conflicts)
	}
	// The service page names both sides, with the namespace, so the two lines are not
	// one owner disagreeing with itself.
	d, err := q.EntityDetail(KindService, "ledger")
	if err != nil {
		t.Fatalf("EntityDetail(service, ledger): %v", err)
	}
	joined := strings.Join(d.Service.Ownership.Conflicts.Items, " ")
	if !strings.Contains(joined, "alice (DRI)") {
		t.Errorf("the conflict preview reads %q, and must name the namespace that makes it a conflict", joined)
	}
}

// C: the regression guard. Same team, different DRI is ONE owner, and always was.
// Namespacing the key must not turn a team's two people into a dispute.
func TestOwnerNamespace_SameTeamDifferentDRIRemainsOneOwner(t *testing.T) {
	q := inventoryFleet(t,
		inventoryRevision{service: "ledger", distinct: "1", target: "prod",
			owner: contract.Owner{Team: "platform", DRI: "alice"}},
		inventoryRevision{service: "ledger", distinct: "2",
			owner: contract.Owner{Team: "platform", DRI: "bob"}},
	)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if want := (OwnershipTally{Consistent: 1}); agg.Ownership != want {
		t.Errorf("ownership = %+v, want %+v: one team, two people, no dispute", agg.Ownership, want)
	}
	if len(agg.ByOwner) != 1 || agg.ByOwner[0].Key != "team:platform" || agg.ByOwner[0].Services != 1 {
		t.Fatalf("ranking = %+v, want the one owner the product routes to", agg.ByOwner)
	}
	if agg.DistinctOwners != 1 || agg.RankedOwners != 1 {
		t.Errorf("distinct=%d ranked=%d, want one owner", agg.DistinctOwners, agg.RankedOwners)
	}
	d, err := q.EntityDetail(KindOwner, "team:platform")
	if err != nil {
		t.Fatalf("EntityDetail(owner, team:platform): %v", err)
	}
	if d.Owner.Summary.Revisions != 2 || d.Owner.Summary.Services != 1 {
		t.Errorf("the platform page holds %d revisions / %d services, want 2 / 1", d.Owner.Summary.Revisions, d.Owner.Summary.Services)
	}
	if d.Service != nil {
		t.Error("owner detail must not carry a service payload")
	}
}

// D: every surface a canonical owner reaches is per-identity. One shared label must
// not leak one owner's services, targets, revisions, backlog, page or ranking row
// into the other's. Split across three helpers per surface family: one function
// walking all of them at once reads as a checklist and scores as a hotspot.
func TestOwnerNamespace_EverySurfaceIsScopedToOneIdentity(t *testing.T) {
	q := sameLabelFleet(t)
	for key, own := range map[string]string{"team:alice": "checkout", "dri:alice": "billing"} {
		d := assertOwnerPageIsOwn(t, q, key, own)
		assertOwnerBacklogIsOwn(t, q, key, own, d.Owner.Attention.Total)
		assertOwnerListsAreOwn(t, q, key)
	}
}

// assertOwnerPageIsOwn checks the owner's own page: how it is identified, and that
// every estate list on it holds only that owner's work.
func assertOwnerPageIsOwn(t *testing.T, q *Query, key, own string) *EntityDetail {
	t.Helper()
	d, err := q.EntityDetail(KindOwner, key)
	if err != nil {
		t.Fatalf("EntityDetail(owner, %s): %v", key, err)
	}
	if d.Entity.Key != key || d.Entity.Label != "alice" {
		t.Errorf("%s's page is identified as %+v; the key routes and the label reads", key, d.Entity)
	}
	if d.Entity.Secondary == "" {
		t.Errorf("%s's page does not say which namespace it is, so it is indistinguishable from the other alice", key)
	}
	if d.Owner.Summary.Services != 1 || d.Owner.Summary.Revisions != 1 || d.Owner.Summary.Targets != 1 {
		t.Errorf("%s's estate is %+v, want exactly its own one of each", key, d.Owner.Summary)
	}
	for _, ref := range d.Owner.Services.Items {
		if ref.Key != own {
			t.Errorf("%s's estate holds %s", key, ref.Key)
		}
	}
	for _, ref := range d.Owner.Deployments.Items {
		if ref.ParentService != own {
			t.Errorf("%s's deployments hold a target of %s", key, ref.ParentService)
		}
	}
	for _, ref := range d.Owner.Revisions.Items {
		if !strings.HasPrefix(ref.Key, own+"@") {
			t.Errorf("%s's revisions hold %s", key, ref.Key)
		}
	}
	return d
}

// assertOwnerBacklogIsOwn checks the backlog the owner actually works from. The
// page's preview total is the TRUE total, so a broadened one shows here even when
// the other owner's items fall past the preview bound.
func assertOwnerBacklogIsOwn(t *testing.T, q *Query, key, own string, pageTotal int) {
	t.Helper()
	att, err := q.Attention(AttentionFilter{OwnerKey: key})
	if err != nil {
		t.Fatalf("Attention(ownerKey=%s): %v", key, err)
	}
	if att.Total == 0 {
		t.Fatal("the fixture raises no attention item, so this proves nothing")
	}
	if pageTotal != att.Total {
		t.Errorf("%s's page shows %d backlog items, its own filter %d", key, pageTotal, att.Total)
	}
	for _, it := range att.Items {
		if it.Service != own {
			t.Errorf("ownerKey=%s's backlog holds an item for %s", key, it.Service)
		}
	}
}

// assertOwnerListsAreOwn checks the list surfaces the identity narrows: the revision
// and target inventories, and the ranking row that promises a drill-down count.
func assertOwnerListsAreOwn(t *testing.T, q *Query, key string) {
	t.Helper()
	for _, kind := range []EntityKind{KindRevision, KindTarget} {
		list, err := q.Entities(EntityFilter{Kinds: []EntityKind{kind}, OwnerKey: key})
		if err != nil {
			t.Fatalf("Entities(%s, ownerKey=%s): %v", kind, key, err)
		}
		if list.Total != 1 {
			t.Errorf("ownerKey=%s matched %d %ss, want its own one", key, list.Total, kind)
		}
	}
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	for _, row := range agg.ByOwner {
		if row.Key != key {
			continue
		}
		got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: row.Key, Ownership: OwnershipConsistent})
		if len(got) != row.Services {
			t.Errorf("%s ranks %d services, its drill-down lists %v", key, row.Services, got)
		}
	}
}

// E: the encoding survives contact with real owner names. A key is built from
// whatever the contract permits, so it must round-trip separators, slashes and
// strings that look like another namespace.
func TestOwnerNamespace_KeysRoundTripAwkwardOwnerNames(t *testing.T) {
	names := []string{"team/payments", "dri:alice", "a:b:c", "external/sendgrid", "with space", "ünïcode"}
	var revs []inventoryRevision
	for i, n := range names {
		revs = append(revs,
			inventoryRevision{service: fmt.Sprintf("t%d", i), distinct: "1", owner: contract.Owner{Team: n}},
			inventoryRevision{service: fmt.Sprintf("d%d", i), distinct: "1", owner: contract.Owner{DRI: n}},
		)
	}
	q := inventoryFleet(t, revs...)
	roster := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindOwner}, Limit: 100})
	if len(roster) != 2*len(names) {
		t.Fatalf("the roster holds %d owners, want %d — two names collapsed into one key", len(roster), 2*len(names))
	}
	for i, n := range names {
		for _, tc := range []struct{ key, svc string }{
			{(contract.OwnerKey{Kind: contract.OwnerKindTeam, Value: n}).String(), fmt.Sprintf("t%d", i)},
			{(contract.OwnerKey{Kind: contract.OwnerKindDRI, Value: n}).String(), fmt.Sprintf("d%d", i)},
		} {
			if !containsStr(roster, tc.key) {
				t.Errorf("the roster is missing %q", tc.key)
			}
			if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: tc.key}); fmt.Sprint(got) != "["+tc.svc+"]" {
				t.Errorf("ownerKey=%q listed %v, want [%s]", tc.key, got, tc.svc)
			}
			// The page opens, and its label is the name as authored — no prefix bleeding
			// into what the reader sees.
			d, err := q.EntityDetail(KindOwner, tc.key)
			if err != nil {
				t.Fatalf("EntityDetail(owner, %q): %v", tc.key, err)
			}
			if d.Entity.Label != n {
				t.Errorf("%q renders as %q, want the name as authored (%q)", tc.key, d.Entity.Label, n)
			}
		}
	}
}

// F: the search is untouched. A reader who types half an owner's name is asking a
// real question, and the namespace is not something they should have to know.
func TestOwnerNamespace_FuzzySearchStillFindsBothAlices(t *testing.T) {
	q := sameLabelFleet(t)
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Owner: "alice"}); fmt.Sprint(got) != "[billing checkout]" {
		t.Errorf("owner=alice found %v, want both owners' services", got)
	}
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindOwner}, Owner: "ALIC"}); fmt.Sprint(got) != "[dri:alice team:alice]" {
		t.Errorf("owner discovery for ALIC listed %v, want both owners", got)
	}
	att, err := q.Attention(AttentionFilter{Owner: "alice"})
	if err != nil {
		t.Fatalf("Attention(owner=alice): %v", err)
	}
	exact, err := q.Attention(AttentionFilter{OwnerKey: "team:alice"})
	if err != nil {
		t.Fatalf("Attention(ownerKey=team:alice): %v", err)
	}
	if exact.Total >= att.Total {
		t.Errorf("the search matched %d items and the canonical key %d; the fixture is no longer a collision", att.Total, exact.Total)
	}
	// The search reaches the NAME, not the encoding: typing the namespace prefix is
	// not how a reader finds an owner, and it must not select a whole namespace.
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindOwner}, Owner: "team:"}); fmt.Sprint(got) != "[]" {
		t.Errorf("owner search for the encoding prefix listed %v, want nothing", got)
	}
}

// OWNERSHIP THAT CANNOT BE NAVIGATED TO STILL HAPPENED.
//
// The contract permits an owner block of contacts alone. That is a real, declared
// owner — an escalation address is not nothing — but there is no name to file it
// under and no page to open, and inventing one from an email address would put a
// name on the fleet that nobody chose. So it stays out of the ranking, and the
// aggregate says so out loud instead of letting the service fall through the gap
// between "ranked" and "consistent".

func contactsOnly(vals ...string) contract.Owner {
	var cs []contract.OwnerContact
	for _, v := range vals {
		cs = append(cs, contract.OwnerContact{Type: "chat", Value: v, Purpose: "support"})
	}
	return contract.Owner{Contacts: cs}
}

// reconcile is the invariant every one of these cases is checked against: the
// ranking's rows, its tail and the unidentifiable remainder account for exactly the
// consistently owned population — no service counted twice, none lost.
func reconcile(t *testing.T, agg EntityAggregate) {
	t.Helper()
	shown := 0
	for _, row := range agg.ByOwner {
		shown += row.Services
	}
	if shown+agg.BeyondRanking+agg.UnidentifiedOwnership != agg.Ownership.Consistent {
		t.Errorf("shown %d + beyond ranking %d + unidentified %d != consistent %d",
			shown, agg.BeyondRanking, agg.UnidentifiedOwnership, agg.Ownership.Consistent)
	}
	if agg.Ownership.Total() != agg.Services {
		t.Errorf("ownership %+v does not partition the %d matched services", agg.Ownership, agg.Services)
	}
	if agg.RankedOwners < len(agg.ByOwner) {
		t.Errorf("rankedOwners %d is under the %d rows shown", agg.RankedOwners, len(agg.ByOwner))
	}
	if agg.DistinctOwners < agg.RankedOwners {
		t.Errorf("distinctOwners %d is under rankedOwners %d; a rankable owner is an owner",
			agg.DistinctOwners, agg.RankedOwners)
	}
	if (agg.RankedOwners == 0) != (len(agg.ByOwner) == 0) {
		t.Errorf("rankedOwners %d disagrees with %d rows about whether anything ranks", agg.RankedOwners, len(agg.ByOwner))
	}
}

// THE arithmetic counterexample: one service, consistently owned, no canonical
// owner. Consistent counted it, the ranking could not, and nothing said so — the
// aggregate's own equation did not close.
func TestUnidentifiedOwnership_ASingleContactsOnlyServiceIsAccountedFor(t *testing.T) {
	q := inventoryFleet(t, inventoryRevision{service: "orphan", distinct: "1", target: "prod", owner: contactsOnly("#sre")})
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	reconcile(t, agg)
	if agg.Ownership.Consistent != 1 || agg.UnidentifiedOwnership != 1 {
		t.Errorf("consistent=%d unidentified=%d, want 1 / 1", agg.Ownership.Consistent, agg.UnidentifiedOwnership)
	}
	if len(agg.ByOwner) != 0 || agg.DistinctOwners != 0 || agg.RankedOwners != 0 {
		t.Errorf("a contacts-only owner reached the owner model: byOwner=%+v distinct=%d ranked=%d",
			agg.ByOwner, agg.DistinctOwners, agg.RankedOwners)
	}
	// It is owned, not unowned: the reader must not be sent to declare what is there.
	if agg.Ownership.Unowned != 0 {
		t.Errorf("ownership = %+v, want the declaration counted as ownership", agg.Ownership)
	}
	// And the service page says ownership was declared rather than rendering Unowned.
	d, err := q.EntityDetail(KindService, "orphan")
	if err != nil {
		t.Fatalf("EntityDetail(service, orphan): %v", err)
	}
	if o := d.Service.Ownership; o == nil || !o.Declared || o.Ref != nil {
		t.Errorf("service ownership = %+v, want declared with no canonical ref", o)
	}
}

// The remaining populations, each checked against the same equation.
func TestUnidentifiedOwnership_ReconcilesAcrossEveryOwnershipPopulation(t *testing.T) {
	manyOwners := func() []inventoryRevision {
		var revs []inventoryRevision
		for owner := 1; owner <= maxOwnerRanking+2; owner++ {
			for svc := 0; svc < owner; svc++ {
				revs = append(revs, inventoryRevision{
					service: fmt.Sprintf("svc-%02d-%02d", owner, svc),
					owner:   contract.Owner{Team: fmt.Sprintf("team-%02d", owner)},
				})
			}
		}
		return revs
	}
	for _, tc := range []struct {
		name         string
		revs         []inventoryRevision
		consistent   int
		unidentified int
		ranked       int
		distinct     int
		beyond       int
	}{{
		name: "several revisions repeating one contacts-only declaration",
		revs: []inventoryRevision{
			{service: "orphan", distinct: "1", owner: contactsOnly("#sre", "sre@acme.com")},
			{service: "orphan", distinct: "2", owner: contactsOnly("#sre", "sre@acme.com")},
			{service: "orphan", distinct: "3", owner: contactsOnly("#sre", "sre@acme.com")},
		},
		consistent: 1, unidentified: 1,
	}, {
		// The list order is authoring incidental. Two revisions that spell the same
		// two contact points in two orders declare one owner, not a dispute.
		name: "the same contacts in a different order",
		revs: []inventoryRevision{
			{service: "orphan", distinct: "1", owner: contactsOnly("#sre", "sre@acme.com")},
			{service: "orphan", distinct: "2", owner: contactsOnly("sre@acme.com", "#sre")},
		},
		consistent: 1, unidentified: 1,
	}, {
		name: "two genuinely different contacts-only claims",
		revs: []inventoryRevision{
			{service: "orphan", distinct: "1", owner: contactsOnly("#one")},
			{service: "orphan", distinct: "2", owner: contactsOnly("#two")},
		},
		consistent: 0, unidentified: 0,
	}, {
		name: "a named owner and a contacts-only one, side by side",
		revs: []inventoryRevision{
			{service: "named", distinct: "1", owner: contract.Owner{Team: "platform"}},
			{service: "orphan", distinct: "1", owner: contactsOnly("#sre")},
			{service: "nobody", distinct: "1"},
		},
		consistent: 2, unidentified: 1, ranked: 1, distinct: 1,
	}, {
		name: "a named owner and a contacts-only owner on ONE service",
		revs: []inventoryRevision{
			{service: "mixed", distinct: "1", owner: contract.Owner{Team: "platform"}},
			{service: "mixed", distinct: "2", owner: contactsOnly("#sre")},
		},
		consistent: 0, unidentified: 0, distinct: 1,
	}, {
		name: "more owners than the ranking shows, plus contacts-only ownership",
		revs: append(manyOwners(),
			inventoryRevision{service: "orphan-a", distinct: "1", owner: contactsOnly("#sre")},
			inventoryRevision{service: "orphan-b", distinct: "1", owner: contactsOnly("#ops")},
		),
		// 1+2+...+12 named services, plus the two unidentifiable ones.
		consistent: 78 + 2, unidentified: 2, ranked: 12, distinct: 12, beyond: 3,
	}, {
		name:       "owners that exist only inside a conflicted service",
		revs:       disputedRevisions(),
		consistent: 2, ranked: 2, distinct: 2,
	}, {
		name: "an owner with NO consistently owned service at all",
		revs: []inventoryRevision{
			{service: "disputed", distinct: "1", owner: contract.Owner{Team: "team-x"}},
			{service: "disputed", distinct: "2", owner: contract.Owner{Team: "team-y"}},
		},
		consistent: 0, ranked: 0, distinct: 2,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			agg := entityAggregateOf(t, inventoryFleet(t, tc.revs...), EntityFilter{Kinds: []EntityKind{KindService}, Limit: 200})
			reconcile(t, agg)
			if agg.Ownership.Consistent != tc.consistent {
				t.Errorf("consistent = %d, want %d (%+v)", agg.Ownership.Consistent, tc.consistent, agg.Ownership)
			}
			if agg.UnidentifiedOwnership != tc.unidentified {
				t.Errorf("unidentifiedOwnership = %d, want %d", agg.UnidentifiedOwnership, tc.unidentified)
			}
			if agg.RankedOwners != tc.ranked {
				t.Errorf("rankedOwners = %d, want %d", agg.RankedOwners, tc.ranked)
			}
			if agg.DistinctOwners != tc.distinct {
				t.Errorf("distinctOwners = %d, want %d", agg.DistinctOwners, tc.distinct)
			}
			if agg.BeyondRanking != tc.beyond {
				t.Errorf("beyondRanking = %d, want %d", agg.BeyondRanking, tc.beyond)
			}
		})
	}
}

// DistinctOwners means EVERY canonical owner the matched services declare, and an
// owner in a dispute is exactly the owner a reader is looking for. Counting only
// the rankable ones would erase them, and making the ranking include disputed
// services to force the numbers to agree would be the other, worse way out: it
// would report a contested service as one owner's.
func TestDistinctOwners_CountsOwnersThatOnlyAppearOnConflictedServices(t *testing.T) {
	q := inventoryFleet(t,
		inventoryRevision{service: "solo", distinct: "1", target: "prod", owner: contract.Owner{Team: "loud"}},
		inventoryRevision{service: "disputed", distinct: "1", owner: contract.Owner{Team: "loud"}},
		// quiet owns nothing outright: it exists only as one side of the dispute.
		inventoryRevision{service: "disputed", distinct: "2", owner: contract.Owner{Team: "quiet"}},
	)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	reconcile(t, agg)
	if agg.DistinctOwners != 2 {
		t.Errorf("distinctOwners = %d, want 2 — quiet is an owner even though it ranks nowhere", agg.DistinctOwners)
	}
	if agg.RankedOwners != 1 || len(agg.ByOwner) != 1 || agg.ByOwner[0].Key != "team:loud" {
		t.Errorf("ranking = %+v (ranked %d), want only the owner with a service of its own",
			agg.ByOwner, agg.RankedOwners)
	}
	if agg.ByOwner[0].Services != 1 {
		t.Errorf("loud ranks %d services; the disputed one is not loud's to claim", agg.ByOwner[0].Services)
	}
	// quiet is nonetheless a real, reachable owner with a page and an estate.
	d, err := q.EntityDetail(KindOwner, "team:quiet")
	if err != nil {
		t.Fatalf("EntityDetail(owner, team:quiet): %v", err)
	}
	if d.Owner.Summary.Services != 1 {
		t.Errorf("quiet's page holds %d services, want the disputed one", d.Owner.Summary.Services)
	}
}

// None of these answers may depend on the order the revisions arrived in.
func TestOwnerNamespace_AnswersAreIndependentOfDeclarationOrder(t *testing.T) {
	forward := []inventoryRevision{
		{service: "checkout", distinct: "1", target: "prod", owner: contract.Owner{Team: "alice"}},
		{service: "billing", distinct: "1", target: "prod", owner: contract.Owner{DRI: "alice"}},
		{service: "orphan", distinct: "1", owner: contactsOnly("#sre", "sre@acme.com")},
		{service: "orphan", distinct: "2", owner: contactsOnly("sre@acme.com", "#sre")},
		{service: "ledger", distinct: "1", owner: contract.Owner{Team: "platform", DRI: "alice"}},
		{service: "ledger", distinct: "2", owner: contract.Owner{Team: "platform", DRI: "bob"}},
	}
	reversed := make([]inventoryRevision, 0, len(forward))
	for i := len(forward) - 1; i >= 0; i-- {
		reversed = append(reversed, forward[i])
	}
	ask := func(revs []inventoryRevision) string {
		q := inventoryFleet(t, revs...)
		agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
		var b []string
		b = append(b, fmt.Sprintf("%+v %+v %d/%d/%d/%d", agg.Ownership, agg.ByOwner,
			agg.BeyondRanking, agg.UnidentifiedOwnership, agg.RankedOwners, agg.DistinctOwners))
		for _, key := range []string{"team:alice", "dri:alice", "team:platform"} {
			b = append(b, fmt.Sprint(serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: key})))
			d, err := q.EntityDetail(KindOwner, key)
			if err != nil {
				t.Fatalf("EntityDetail(owner, %s): %v", key, err)
			}
			b = append(b, fmt.Sprintf("%+v", d.Owner.Summary))
		}
		var lims []string
		for _, l := range q.snap.Limitations {
			lims = append(lims, string(l.Code))
		}
		sort.Strings(lims)
		return fmt.Sprint(b, lims)
	}
	if a, z := ask(forward), ask(reversed); a != z {
		t.Errorf("declaration order changed the answers:\n forward: %s\nreversed: %s", a, z)
	}
}
