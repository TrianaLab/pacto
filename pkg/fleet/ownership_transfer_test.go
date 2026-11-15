package fleet

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// A sequential owner handover (v1 owned by team-a, v2 by team-b) is a change of
// owner, never a conflict. A conflict is a disagreement that holds now: targets
// running revisions that declare different owners, or two revisions of the
// same version that do.

type transferRev struct {
	version, team, domain string
}

type transferTarget struct {
	scope, version string
	exact          bool // linked by digest; otherwise by tag (inferred)
}

// transferFleet builds the service "shop" from revisions and targets that run
// them, plus one bystander "other" owned by team-c.
func transferFleet(t *testing.T, revs []transferRev, targets []transferTarget) *Query {
	t.Helper()
	var raw []RawRevision
	digests := map[string]string{}
	for i, r := range revs {
		var owner contract.Owner
		if r.team != "" {
			owner = contract.Owner{Team: r.team}
		}
		d := fmt.Sprintf("sha256:shop%d", i)
		digests[r.version] = d
		raw = append(raw, RawRevision{Digest: d, Bundle: &contract.Bundle{Contract: &contract.Contract{
			PactoVersion: "2.0", Service: contract.Service{Name: "shop", Version: r.version, Owner: owner, Domain: r.domain},
		}}})
	}
	raw = append(raw, RawRevision{Digest: "sha256:other", Bundle: &contract.Bundle{Contract: &contract.Contract{
		PactoVersion: "2.0", Service: contract.Service{Name: "other", Version: "1.0.0", Owner: contract.Owner{Team: "team-c"}},
	}}})
	var raws []RawTarget
	for _, tg := range targets {
		rt := RawTarget{Scope: tg.scope, Kind: SourceKindKubernetes, Name: "shop-" + tg.scope, Service: "shop",
			Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())}
		if tg.exact {
			rt.Digest = digests[tg.version]
		} else {
			rt.ResolvedRef = "oci://registry.example/shop:" + tg.version
		}
		raws = append(raws, rt)
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{Revisions: raw, Targets: raws}))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return NewQuery(snap)
}

var handover = []transferRev{{version: "1.0.0", team: "team-a", domain: "shop"}, {version: "2.0.0", team: "team-b", domain: "shop"}}

func ownerConflictMessages(q *Query) []string {
	var out []string
	for _, l := range q.snap.Limitations {
		if l.Code == LimitationOwnerConflict {
			out = append(out, l.Message)
		}
	}
	return out
}

func shopDetail(t *testing.T, q *Query) *ServiceDetailData {
	t.Helper()
	d, err := q.EntityDetail(KindService, string(NewServiceKey("shop")))
	if err != nil {
		t.Fatal(err)
	}
	return d.Service
}

func TestOwnerTransferIsNotConflict(t *testing.T) {
	q := transferFleet(t, handover, nil)
	if m := ownerConflictMessages(q); len(m) != 0 {
		t.Errorf("a handover raised %v", m)
	}
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if agg.Ownership.Consistent != 2 || agg.Ownership.Conflicting != 0 {
		t.Errorf("ownership = %+v, want both services consistent", agg.Ownership)
	}
	if n, key := q.ownershipState(q.snap.Services[NewServiceKey("shop")]); n != 1 || key != "team:team-b" {
		t.Errorf("claims = %d %q, want 1 team:team-b", n, key)
	}
}

func TestOwnerConflictTargetsDisagree(t *testing.T) {
	q := transferFleet(t, handover, []transferTarget{{scope: "eu", version: "1.0.0", exact: true}, {scope: "us", version: "2.0.0", exact: true}})
	m := ownerConflictMessages(q)
	if len(m) != 1 || m[0] != "targets of shop run revisions that declare different owners" {
		t.Errorf("conflicts = %q", m)
	}
}

func TestOwnerConflictInferredLinkSaysInferred(t *testing.T) {
	q := transferFleet(t, handover, []transferTarget{{scope: "eu", version: "1.0.0"}, {scope: "us", version: "2.0.0", exact: true}})
	m := ownerConflictMessages(q)
	if len(m) != 1 || m[0] != "targets of shop are linked by tag (inferred) to revisions that declare different owners" {
		t.Errorf("conflicts = %q", m)
	}
}

func TestOwnerConflictSameVersion(t *testing.T) {
	q := transferFleet(t, []transferRev{{version: "1.0.0", team: "team-a"}, {version: "2.0.0", team: "team-a"}, {version: "2.0.0", team: "team-b"}}, nil)
	m := ownerConflictMessages(q)
	if len(m) != 1 || m[0] != "two revisions of shop 2.0.0 declare different owners" {
		t.Errorf("conflicts = %q", m)
	}
	if n, _ := q.ownershipState(q.snap.Services[NewServiceKey("shop")]); n != 2 {
		t.Errorf("claims = %d, want 2", n)
	}
}

func TestOwnerTransferRanksUnderNewOwner(t *testing.T) {
	q := transferFleet(t, handover, nil)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	rows := map[string]int{}
	for _, r := range agg.ByOwner {
		rows[r.Key] = r.Services
	}
	if rows["team:team-b"] != 1 || rows["team:team-a"] != 0 {
		t.Errorf("byOwner = %+v, want shop under team:team-b only", agg.ByOwner)
	}
	if _, ok := rows["team:team-a"]; ok {
		t.Errorf("former owner ranked: %+v", agg.ByOwner)
	}
}

func TestOwnerFilterFollowsCurrentClaims(t *testing.T) {
	q := transferFleet(t, handover, nil)
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: "team:team-a"}); len(got) != 0 {
		t.Errorf("ownerKey=team:team-a listed %v", got)
	}
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: "team:team-b"}); fmt.Sprint(got) != "[shop]" {
		t.Errorf("ownerKey=team:team-b listed %v", got)
	}
}

func TestFormerOwnerPageKeepsRevisions(t *testing.T) {
	q := transferFleet(t, handover, nil)
	d, err := q.EntityDetail(KindOwner, "team:team-a")
	if err != nil {
		t.Fatal(err)
	}
	if d.Owner.Services.Total != 0 || d.Owner.Revisions.Total != 1 || d.Owner.Revisions.Items[0].Version != "1.0.0" {
		t.Errorf("former owner page: services %d, revisions %+v", d.Owner.Services.Total, d.Owner.Revisions.Items)
	}
	if d.Owner.Summary.Services != 0 {
		t.Errorf("former owner summary counts %d services", d.Owner.Summary.Services)
	}
}

func TestRestrictedOwnerTransfer(t *testing.T) {
	// The only target that runs v1 is in a namespace the caller may not read:
	// the restricted view re-derives the rule from what it still holds, so the
	// dispute the full view reports is a plain handover there.
	q := transferFleet(t, handover, []transferTarget{{scope: "secret", version: "1.0.0", exact: true}, {scope: "open", version: "2.0.0", exact: true}})
	if len(ownerConflictMessages(q)) != 1 {
		t.Fatalf("full view conflicts = %v", ownerConflictMessages(q))
	}
	r := q.Restrict(func(ns string) bool { return ns != "secret" })
	if m := ownerConflictMessages(r); len(m) != 0 {
		t.Errorf("restricted view conflicts = %v", m)
	}
	if r.snap.Completeness != CompletenessPartial {
		t.Errorf("restricted completeness = %q", r.snap.Completeness)
	}
	if n, key := r.ownershipState(r.snap.Services[NewServiceKey("shop")]); n != 1 || key != "team:team-b" {
		t.Errorf("restricted claims = %d %q", n, key)
	}
	if d := shopDetail(t, r); d.Ownership.Conflicts.Count != 0 || d.Ownership.ConflictReason != "" {
		t.Errorf("restricted ownership = %+v", d.Ownership)
	}
}

func TestOwnershipFilterAfterTransfer(t *testing.T) {
	q := transferFleet(t, handover, nil)
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipConflicting}); len(got) != 0 {
		t.Errorf("ownership=conflicting listed %v", got)
	}
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipConsistent}); fmt.Sprint(got) != "[other shop]" {
		t.Errorf("ownership=consistent listed %v", got)
	}
}

func TestOwnersInDomainAfterTransfer(t *testing.T) {
	q := transferFleet(t, handover, nil)
	got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindOwner}, BusinessDomain: "shop"})
	if fmt.Sprint(got) != "[team:team-b]" {
		t.Errorf("owners in domain shop = %v, want [team:team-b]", got)
	}
}

func TestServiceOwnershipConflictsCurrentClaimsOnly(t *testing.T) {
	if d := shopDetail(t, transferFleet(t, handover, nil)); d.Ownership.Conflicts.Count != 0 || d.Ownership.ConflictReason != "" {
		t.Errorf("handover ownership = %+v", d.Ownership)
	}
	q := transferFleet(t, handover, []transferTarget{{scope: "eu", version: "1.0.0", exact: true}, {scope: "us", version: "2.0.0", exact: true}})
	d := shopDetail(t, q)
	if d.Ownership.Conflicts.Count != 2 || d.Ownership.ConflictReason != "targets of shop run revisions that declare different owners" {
		t.Errorf("target dispute ownership = %+v", d.Ownership)
	}
	for _, it := range d.Ownership.Conflicts.Items {
		if !strings.HasPrefix(it, "shop@") || !strings.Contains(it, "team-") {
			t.Errorf("conflict %q names no revision and owner", it)
		}
	}
}

// Characterization: with no revision declaring an owner, the owners the
// targets' observing sources report stand in, as before.
func TestOwnerTargetFallbackUnchanged(t *testing.T) {
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "a", Kind: SourceKindKubernetes, Name: "x-a", Service: "x", Owner: contract.Owner{Team: "ops"}, Compliance: StatusCompliant},
		{Scope: "b", Kind: SourceKindKubernetes, Name: "x-b", Service: "x", Owner: contract.Owner{Team: "ops"}, Compliance: StatusCompliant},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	if n, key := q.ownershipState(snap.Services[NewServiceKey("x")]); n != 1 || key != "team:ops" {
		t.Errorf("fallback claims = %d %q", n, key)
	}
	if m := ownerConflictMessages(q); len(m) != 0 {
		t.Errorf("fallback conflicts = %v", m)
	}
}

// has answers by the identity add uses: the canonical key, else the contact block.
func TestOwnerClaimSetHas(t *testing.T) {
	var c ownerClaimSet
	ops := contract.Owner{Contacts: []contract.OwnerContact{{Type: "email", Value: "ops@example.com"}}}
	c.add(contract.Owner{Team: "a"})
	c.add(ops)
	if !c.has(contract.Owner{Team: "a", DRI: "x"}) || c.has(contract.Owner{DRI: "a"}) {
		t.Errorf("keyed has = %+v", c)
	}
	if !c.has(ops) || c.has(contract.Owner{Contacts: []contract.OwnerContact{{Type: "email", Value: "dev@example.com"}}}) {
		t.Errorf("keyless has = %+v", c)
	}
}

// W-3: a former owner stays in the owner list, so its page and the revisions it
// declared stay reachable, while rankings count current claims only.
func TestFormerOwnerStaysListed(t *testing.T) {
	q := transferFleet(t, handover, nil)
	if got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindOwner}}); fmt.Sprint(got) != "[team:team-a team:team-b team:team-c]" {
		t.Errorf("owners = %v, want the former owner team-a listed beside team-b and the other service's team-c", got)
	}
}
