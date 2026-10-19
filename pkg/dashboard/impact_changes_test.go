package dashboard

import (
	"context"
	"net/http"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// The Change-analysis workspace shows the field-level semantic diff AND the blast
// radius from ONE canonical revision pair, so the product impact answer must carry
// the whole change, ordered breaking first and bounded.
func TestProductImpact_ChangesArePartitionedOrderedAndCounted(t *testing.T) {
	q := twoDomainDashboardQuery(t)
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "domain-a")
	res := &impact.Result{
		SchemaVersion: impact.SchemaVersion, SnapshotID: snapID, Service: "payment-service", Classification: "BREAKING",
		BreakingChanges: []diff.Change{
			{Path: "interfaces.http.paths./pay.get", Type: diff.Removed, Classification: diff.Breaking, Reason: "operation removed", OldValue: "get /pay"},
		},
		PotentiallyBreakingChanges: []diff.Change{
			{Path: "configuration.timeout", Type: diff.Modified, Classification: diff.PotentialBreaking, OldValue: 30, NewValue: 5},
		},
		NonBreakingChanges: []diff.Change{
			{Path: "service.description", Type: diff.Added, Classification: diff.NonBreaking, NewValue: "payments"},
		},
	}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()

	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: from, ToRevisionKey: from}, http.StatusOK, &out)

	ch := out.Changes
	if ch.Total != 3 || ch.Count != 3 || ch.Truncated {
		t.Errorf("counts = total %d count %d truncated %v, want 3/3/false", ch.Total, ch.Count, ch.Truncated)
	}
	if ch.Breaking != 1 || ch.Potential != 1 || ch.NonBreaking != 1 {
		t.Errorf("classification counts = %d/%d/%d, want 1/1/1", ch.Breaking, ch.Potential, ch.NonBreaking)
	}
	// Breaking first, then potentially breaking, then non-breaking.
	want := []string{"BREAKING", "POTENTIAL_BREAKING", "NON_BREAKING"}
	for i, w := range want {
		if ch.Items[i].Classification != w {
			t.Errorf("item %d classification = %q, want %q", i, ch.Items[i].Classification, w)
		}
	}
	if ch.Items[0].Type != "removed" || ch.Items[0].Reason != "operation removed" {
		t.Errorf("breaking item lost its type/reason: %+v", ch.Items[0])
	}
	// Values are rendered as display strings, never shipped untyped.
	if ch.Items[1].OldValue != "30" || ch.Items[1].NewValue != "5" {
		t.Errorf("numeric values not rendered: %+v", ch.Items[1])
	}
	if ch.Items[2].OldValue != "" || ch.Items[2].NewValue != "payments" {
		t.Errorf("added change values wrong: %+v", ch.Items[2])
	}
}

// A change set larger than the bound truncates HONESTLY: Total still counts every
// change found, so a truncated preview never reads as the complete change.
func TestProductImpact_ChangesTruncateHonestly(t *testing.T) {
	q := twoDomainDashboardQuery(t)
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "domain-a")
	many := make([]diff.Change, impact.MaxProductChanges+5)
	for i := range many {
		many[i] = diff.Change{Path: "p", Type: diff.Modified, Classification: diff.NonBreaking}
	}
	res := &impact.Result{
		SchemaVersion: impact.SchemaVersion, SnapshotID: snapID, Service: "payment-service", Classification: "NON_BREAKING",
		BreakingChanges:    []diff.Change{{Path: "gone", Type: diff.Removed, Classification: diff.Breaking}},
		NonBreakingChanges: many,
	}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()

	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: from, ToRevisionKey: from}, http.StatusOK, &out)

	if !out.Changes.Truncated {
		t.Fatal("an over-bound change set must report truncation")
	}
	if out.Changes.Count != impact.MaxProductChanges {
		t.Errorf("count = %d, want the bound %d", out.Changes.Count, impact.MaxProductChanges)
	}
	if out.Changes.Total != impact.MaxProductChanges+6 {
		t.Errorf("total = %d, want every change found (%d)", out.Changes.Total, impact.MaxProductChanges+6)
	}
	// The breaking change is never the one dropped: it sorts first.
	if out.Changes.Items[0].Classification != "BREAKING" {
		t.Errorf("truncation dropped the breaking change first: %+v", out.Changes.Items[0])
	}
}

// An empty diff yields an empty, non-nil preview -- never a null the client has to
// guard, and never a missing "no differences" answer.
func TestProductImpact_ChangesEmptyIsAnAnswer(t *testing.T) {
	q := twoDomainDashboardQuery(t)
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "domain-a")
	res := &impact.Result{SchemaVersion: impact.SchemaVersion, SnapshotID: snapID, Service: "payment-service", Classification: "NON_BREAKING"}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()

	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: from, ToRevisionKey: from}, http.StatusOK, &out)
	if out.Changes.Total != 0 || out.Changes.Count != 0 || out.Changes.Truncated || out.Changes.Items == nil {
		t.Errorf("empty change preview = %+v, want zeroed with a non-nil items slice", out.Changes)
	}
}
