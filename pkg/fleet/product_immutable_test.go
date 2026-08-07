package fleet

import (
	"testing"
	"time"
)

// The product-query answers are part of a public Go package, so their
// immutability cannot rely on an HTTP JSON boundary. Every answer must be fully
// independent of the snapshot AND of any later answer: mutating a returned map,
// slice, pointer or struct must change neither the snapshot nor a re-query
// (requirement: product-query immutability). These regression tests obtain an
// answer, snapshot the pristine form, mutate it deeply, then prove the snapshot
// and a second identical query are both unchanged.

// mutateSources deeply mutates every field of a SourceState slice, including the
// pointer targets, so an aliased slice element or shared pointer is caught.
func mutateSources(ss []SourceState) {
	for i := range ss {
		ss[i].ID = "hacked"
		ss[i].Kind = "hacked"
		ss[i].Status = "hacked"
		ss[i].RevisionCount = -1
		if ss[i].Error != nil {
			ss[i].Error.Code = "hacked"
			ss[i].Error.Message = "hacked"
		}
		if ss[i].LastSuccessfulSync != nil {
			*ss[i].LastSuccessfulSync = time.Unix(0, 0)
		}
		if ss[i].ObservedAt != nil {
			*ss[i].ObservedAt = time.Unix(0, 0)
		}
	}
}

// mutateLimitations mutates every field of a Limitation slice.
func mutateLimitations(ls []Limitation) {
	for i := range ls {
		ls[i].Code = "hacked"
		ls[i].Source = "hacked"
		ls[i].Message = "hacked"
	}
}

// mutateEntityRef mutates every field of an EntityRef in place.
func mutateEntityRef(r *EntityRef) {
	r.Key = "hacked"
	r.Label = "hacked"
	r.Secondary = "hacked"
	r.Status = "hacked"
	r.Domain = "hacked"
}

func TestOverview_Immutable(t *testing.T) {
	q := productFleet(t)
	r1 := q.Overview()
	want := mustJSON(t, r1)
	snapBefore := mustJSON(t, q.snap)

	mutateSources(r1.Meta.Sources)
	mutateLimitations(r1.Meta.Limitations)
	for i := range r1.Attention {
		mutateEntityRef(&r1.Attention[i].Entity)
		r1.Attention[i].Label = "hacked"
	}
	for i := range r1.RecentEvidence {
		mutateEntityRef(&r1.RecentEvidence[i].Target)
		if r1.RecentEvidence[i].At != nil {
			*r1.RecentEvidence[i].At = time.Unix(0, 0)
		}
	}
	for i := range r1.EntryPoints {
		r1.EntryPoints[i].Label = "hacked"
	}

	if after := mustJSON(t, q.snap); after != snapBefore {
		t.Error("mutating an Overview answer changed the snapshot")
	}
	if got := mustJSON(t, q.Overview()); got != want {
		t.Error("a second Overview answer differs from the first (shared state leaked)")
	}
}

func TestEntities_Immutable(t *testing.T) {
	q := productFleet(t)
	r1, err := q.Entities(EntityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := mustJSON(t, r1)
	snapBefore := mustJSON(t, q.snap)

	mutateSources(r1.Meta.Sources)
	mutateLimitations(r1.Meta.Limitations)
	for i := range r1.Entities {
		mutateEntityRef(&r1.Entities[i])
	}

	if after := mustJSON(t, q.snap); after != snapBefore {
		t.Error("mutating an Entities answer changed the snapshot")
	}
	r2, err := q.Entities(EntityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, r2); got != want {
		t.Error("a second Entities answer differs from the first (shared state leaked)")
	}
}

func TestAttention_Immutable(t *testing.T) {
	q := productFleet(t)
	r1, err := q.Attention(AttentionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := mustJSON(t, r1)
	snapBefore := mustJSON(t, q.snap)

	mutateSources(r1.Meta.Sources)
	mutateLimitations(r1.Meta.Limitations)
	for i := range r1.Items {
		mutateEntityRef(&r1.Items[i].Entity)
		r1.Items[i].Label = "hacked"
	}

	if after := mustJSON(t, q.snap); after != snapBefore {
		t.Error("mutating an Attention answer changed the snapshot")
	}
	r2, err := q.Attention(AttentionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, r2); got != want {
		t.Error("a second Attention answer differs from the first (shared state leaked)")
	}
}

func TestNeighborhood_Immutable(t *testing.T) {
	q := productFleet(t)
	r1, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(NewServiceKey("alpha")), Direction: DirectionBoth, Views: allViews()})
	if err != nil {
		t.Fatal(err)
	}
	want := mustJSON(t, r1)
	snapBefore := mustJSON(t, q.snap)

	mutateSources(r1.Meta.Sources)
	mutateLimitations(r1.Meta.Limitations)
	mutateEntityRef(&r1.RequestedFocus)
	mutateEntityRef(&r1.FocusService)
	for i := range r1.Nodes {
		mutateEntityRef(&r1.Nodes[i].Ref)
	}
	for i := range r1.Edges {
		mutateEntityRef(&r1.Edges[i].From)
		mutateEntityRef(&r1.Edges[i].To)
	}

	if after := mustJSON(t, q.snap); after != snapBefore {
		t.Error("mutating a Neighborhood answer changed the snapshot")
	}
	r2, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(NewServiceKey("alpha")), Direction: DirectionBoth, Views: allViews()})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, r2); got != want {
		t.Error("a second Neighborhood answer differs from the first (shared state leaked)")
	}
}
