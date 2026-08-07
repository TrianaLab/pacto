package fleet

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// ownerConflictFleet builds one service whose two revisions declare different
// owners. deriveOwner makes the service summary owner the lowest-keyed revision's
// owner; the other owner therefore lives ONLY on a revision (a revision-only
// owner), which owner search must still discover.
func ownerConflictFleet(t *testing.T) *Query {
	t.Helper()
	mk := func(team, digest string) RawRevision {
		return RawRevision{
			Bundle: &contract.Bundle{Contract: &contract.Contract{
				PactoVersion: "2.0",
				Service:      contract.Service{Name: "multi", Version: "1.0.0", Owner: contract.Owner{Team: team}},
				Readiness:    readyContract(),
			}, FS: fstest.MapFS{}},
			Digest: digest,
		}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{
			mk("team-x", "sha256:aaa"),
			mk("team-y", "sha256:bbb"),
		}}))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestEntities_RevisionOnlyOwnerDiscoverable(t *testing.T) {
	q := ownerConflictFleet(t)
	list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOwner}})
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]bool{}
	for _, e := range list.Entities {
		owners[e.Key] = true
	}
	if !owners["team-x"] || !owners["team-y"] {
		t.Errorf("both a service-summary owner and a revision-only owner must be discoverable, got %v", owners)
	}

	// The revision-only owner filters back to its revision.
	byOwner, err := q.Entities(EntityFilter{Owner: "team-y"})
	if err != nil {
		t.Fatal(err)
	}
	sawRevision := false
	for _, e := range byOwner.Entities {
		if e.Kind == KindRevision {
			sawRevision = true
		}
	}
	if !sawRevision {
		t.Errorf("a revision-only owner filter must return that revision, got %+v", byOwner.Entities)
	}
}

func TestEntities_StructuredOwnerMatching(t *testing.T) {
	// A service owned by team "team-a" with DRI "alice": the display string is
	// "team-a", so an exact-display filter for "alice" would miss it. Structured
	// matching must find it by DRI.
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{{
			Bundle: &contract.Bundle{Contract: &contract.Contract{
				PactoVersion: "2.0",
				Service:      contract.Service{Name: "svc", Version: "1.0.0", Owner: contract.Owner{Team: "team-a", DRI: "alice"}},
				Readiness:    readyContract(),
			}, FS: fstest.MapFS{}},
			Digest: "sha256:svc",
		}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	byDRI, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Owner: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if byDRI.Total != 1 {
		t.Errorf("structured owner match by DRI must find the service, got %d", byDRI.Total)
	}
}

func TestEntities_SourceHealthFilter(t *testing.T) {
	q := productFleet(t)
	partial, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, SourceHealth: string(SourcePartial)})
	if err != nil {
		t.Fatal(err)
	}
	if partial.Total != 1 || partial.Entities[0].Status != string(SourcePartial) {
		t.Errorf("sourceHealth=partial must return the partial source, got %+v", partial.Entities)
	}
	stale, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, SourceHealth: string(SourceStale)})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Total != 1 {
		t.Errorf("sourceHealth=stale = %d, want 1", stale.Total)
	}
	// An unknown source-health value is a typed error, not silently ignored.
	var iqe *InvalidQueryError
	if _, err := q.Entities(EntityFilter{SourceHealth: "bogus"}); !errors.As(err, &iqe) {
		t.Errorf("an invalid source health must be a typed InvalidQueryError, got %v", err)
	}
}

func TestEntities_InvalidComboIs422(t *testing.T) {
	q := productFleet(t)
	var iqe *InvalidQueryError
	// owner does not apply to sources.
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, Owner: "x"}); !errors.As(err, &iqe) {
		t.Error("owner filter on sources-only must be a typed 422, not a silent empty result")
	}
	// scope only applies to targets.
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOwner}, Scope: "prod"}); !errors.As(err, &iqe) {
		t.Error("scope filter on owners-only must be a typed 422")
	}
	// sourceHealth only applies to sources.
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, SourceHealth: string(SourceAvailable)}); !errors.As(err, &iqe) {
		t.Error("sourceHealth filter on services-only must be a typed 422")
	}
	// A valid combo across all kinds never 422s.
	if _, err := q.Entities(EntityFilter{Owner: "team-a"}); err != nil {
		t.Errorf("owner filter across all kinds must be valid, got %v", err)
	}
}
