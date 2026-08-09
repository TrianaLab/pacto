package fleet

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
)

// The tallies exist so a product surface can draw a proportion without counting a
// bounded preview. That only holds if their buckets are EXHAUSTIVE: every member of
// the population lands in exactly one bucket, and Total() is the population. These
// tests pin that property per tally, including the catch-all buckets that only a
// value outside the known set reaches.

func TestComplianceTally_BucketsArePartitionOfThePopulation(t *testing.T) {
	var c ComplianceTally
	for _, s := range []string{
		StatusCompliant, StatusNonCompliant, StatusNonCompliant, StatusUnknown,
		StatusInvalid, "SomethingElse", "",
	} {
		c.add(s)
	}
	want := ComplianceTally{Compliant: 1, NonCompliant: 2, Unknown: 1, Invalid: 1, Other: 2}
	if c != want {
		t.Errorf("tally = %+v, want %+v", c, want)
	}
	if c.Total() != 7 {
		t.Errorf("Total() = %d, want 7 (the whole population, so a distribution has a denominator)", c.Total())
	}
}

func TestLinkTally_BucketsArePartitionOfThePopulation(t *testing.T) {
	var l LinkTally
	l.add(&TargetRecord{RevisionMatch: revisionMatchExact})
	l.add(&TargetRecord{RevisionMatch: revisionMatchInferred})
	l.add(&TargetRecord{Limitations: []Limitation{{Code: LimitationRevisionAmbiguous}}})
	l.add(&TargetRecord{})
	want := LinkTally{Exact: 1, Inferred: 1, Ambiguous: 1, Unresolved: 1}
	if l != want {
		t.Errorf("tally = %+v, want %+v", l, want)
	}
	if l.Total() != 4 {
		t.Errorf("Total() = %d, want 4", l.Total())
	}
}

func TestSeverityTally_BucketsArePartitionOfThePopulation(t *testing.T) {
	var s SeverityTally
	for _, sev := range []finding.Severity{
		finding.SeverityError, finding.SeverityWarning, finding.SeverityWarning,
		finding.SeverityInfo, finding.Severity("whatever"),
	} {
		s.add(sev)
	}
	want := SeverityTally{Errors: 1, Warnings: 2, Infos: 1, Unknown: 1}
	if s != want {
		t.Errorf("tally = %+v, want %+v", s, want)
	}
	if s.Total() != 5 {
		t.Errorf("Total() = %d, want 5", s.Total())
	}
}

// Fresh / stale / no-evidence must partition the population: a target nobody has
// observed is NOT stale evidence, it is no evidence, and folding the two together
// would report an observation that was never made.
func TestEvidenceWindow_NoEvidenceIsNotStaleEvidence(t *testing.T) {
	oldest := fixedNow().Add(-48 * time.Hour)
	newest := fixedNow().Add(-1 * time.Minute)
	mid := fixedNow().Add(-2 * time.Hour)

	var w EvidenceWindow
	w.add(&TargetRecord{EvidenceAt: &mid})
	w.add(&TargetRecord{EvidenceAt: &oldest, Stale: true})
	w.add(&TargetRecord{EvidenceAt: &newest, Quarantined: true})
	w.add(&TargetRecord{}) // never observed

	if w.WithEvidence != 3 || w.WithoutEvidence != 1 {
		t.Errorf("evidence split = %d with / %d without, want 3/1", w.WithEvidence, w.WithoutEvidence)
	}
	if w.Stale != 1 || w.Quarantined != 1 {
		t.Errorf("stale/quarantined = %d/%d, want 1/1", w.Stale, w.Quarantined)
	}
	if w.WithEvidence+w.WithoutEvidence != 4 {
		t.Errorf("the buckets must cover the population, got %d of 4", w.WithEvidence+w.WithoutEvidence)
	}
	if w.Oldest == nil || !w.Oldest.Equal(oldest) {
		t.Errorf("oldest = %v, want %v", w.Oldest, oldest)
	}
	if w.Newest == nil || !w.Newest.Equal(newest) {
		t.Errorf("newest = %v, want %v", w.Newest, newest)
	}
}

// A service whose only revision is invalid and whose revisions are unused is the
// case where "how much of what we know is actually running" answers zero. The
// summary must say so from the COMPLETE populations, not from a preview.
func TestServiceSummary_CountsInvalidAndUnusedRevisions(t *testing.T) {
	q := productFleet(t)
	d, err := q.EntityDetail(KindService, "bad-svc")
	if err != nil {
		t.Fatal(err)
	}
	sum := d.Service.Summary
	if sum.Revisions != 1 || sum.InvalidRevisions != 1 {
		t.Errorf("revisions = %d (%d invalid), want 1 (1 invalid)", sum.Revisions, sum.InvalidRevisions)
	}
	if sum.RevisionsInUse != 0 {
		t.Errorf("no target runs this service, so RevisionsInUse = %d, want 0", sum.RevisionsInUse)
	}
	if sum.Targets != 0 || sum.Compliance.Total() != 0 || sum.Links.Total() != 0 {
		t.Errorf("a service with no targets must tally an empty population: %+v", sum)
	}
}

// A target has no owner of its own; it borrows its logical service's. When the
// service record is missing there is nobody to page, and the block must be absent
// rather than an empty owner that reads as "unowned".
func TestTargetOwnership_AbsentWithoutAServiceRecord(t *testing.T) {
	if got := targetOwnership(nil); got != nil {
		t.Errorf("targetOwnership(nil) = %+v, want nil", got)
	}
}

// Owners and sources have no parent service, so a service filter can never match
// one -- it must exclude them rather than fall through to a key comparison.
func TestEntityInService_KindsWithoutAParentServiceNeverMatch(t *testing.T) {
	for _, kind := range []EntityKind{KindOwner, KindSource} {
		r := EntityRef{Kind: kind, Key: "alpha", ParentService: "alpha"}
		if entityInService(r, "alpha") {
			t.Errorf("%s must not match a service filter", kind)
		}
	}
	if !entityInService(EntityRef{Kind: KindTarget, ParentService: "alpha"}, "alpha") {
		t.Error("a target of the service must match")
	}
}

// The declared content of a revision (policies, capabilities and their bindings)
// has to reach the product detail: "3 policies" with no way to see which three is
// the information loss the contract inspector exists to undo.
func TestRevisionDetail_ProjectsDeclaredPoliciesAndCapabilities(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "guarded", Version: "1.0.0", Owner: contract.Owner{Team: "sec"}},
		Interfaces:   []contract.Interface{{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"}},
		Policies: []contract.Policy{
			{Name: "pii", Schema: "policy/schema.json", Target: "data"},
			{Name: "retention", Ref: "oci://x/retention"},
		},
		Capabilities: []contract.Capability{
			{Type: contract.CapabilityHealth, Binding: &contract.CapabilityBinding{
				Type: contract.CapabilityBindingHTTP, Interface: "http", Path: "/healthz",
			}},
			{Type: contract.CapabilityMetrics},
		},
	}
	src := NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{{
		Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{"interfaces/openapi.json": {Data: []byte(smallOpenAPI)}}},
		Digest: "sha256:guarded",
	}}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	d, err := q.EntityDetail(KindRevision, revKeyOf(t, q, "guarded"))
	if err != nil {
		t.Fatal(err)
	}

	pol := d.Revision.Policies
	if pol.Total != 2 || pol.Count != 2 || pol.Truncated {
		t.Fatalf("policies preview = %+v, want 2 of 2 untruncated", pol)
	}
	if pol.Items[0].Name != "pii" || pol.Items[0].Schema != "policy/schema.json" || pol.Items[0].Target != "data" {
		t.Errorf("declared policy lost its content: %+v", pol.Items[0])
	}
	if pol.Items[1].Ref != "oci://x/retention" {
		t.Errorf("a referenced policy must keep its ref: %+v", pol.Items[1])
	}

	caps := d.Revision.Capabilities
	if caps.Total != 2 || caps.Count != 2 {
		t.Fatalf("capabilities preview = %+v, want 2 of 2", caps)
	}
	b := caps.Items[0].Binding
	if b == nil || b.Type != contract.CapabilityBindingHTTP || b.Interface != "http" || b.Path != "/healthz" {
		t.Errorf("capability binding lost: %+v", caps.Items[0])
	}
	if caps.Items[1].Binding != nil {
		t.Errorf("an unbound capability must not gain a binding: %+v", caps.Items[1])
	}
}
